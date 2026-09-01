package main

import (
	"strings"
	"testing"
)

// The case this program exists for, written first because it is the one that
// went unnoticed for hours: a peer that is registered, tagged, addressed and
// online, and carries nothing.
//
// Every cheap check available at the time reported this peer as healthy. The
// verdict must not.
func TestRegisteredAndUnreachableIsAFailure(t *testing.T) {
	v := Verdict{Results: []result{
		{Name: "hypervisor", IP: "100.64.0.1", Registered: true, Reachable: false, Detail: "no reply"},
		{Name: "site0-cp-100", IP: "100.64.0.10", Registered: true, Reachable: true, Via: "relay ord"},
	}}

	bad := v.unreachable()
	if len(bad) != 1 || bad[0].Name != "hypervisor" {
		t.Fatalf("a registered peer that does not answer was not reported: %+v", bad)
	}
}

// A peer that is honestly offline is not the same failure and must not be
// counted as one. It is visible in the report, because a peer that vanished is
// worth seeing, but it is not the silent case this fails on.
func TestAnHonestlyOfflinePeerIsNotAFailure(t *testing.T) {
	v := Verdict{Results: []result{
		{Name: "retired", IP: "100.64.0.9", Registered: false, Reachable: false},
	}}
	if len(v.unreachable()) != 0 {
		t.Error("a peer that reports itself offline was counted as an unreachable one")
	}
}

// A relayed path is a working path. Failing on it would make the check fire
// constantly on a network that blocks UDP, which is common and survivable.
func TestARelayedPathCounts(t *testing.T) {
	v := Verdict{Results: []result{
		{Name: "peer", Registered: true, Reachable: true, Via: "relay ord"},
	}}
	if len(v.unreachable()) != 0 {
		t.Error("a peer reached through a relay was treated as unreachable")
	}
}

func TestParseStatusExcludesSelfAndOrdersPeers(t *testing.T) {
	raw := []byte(`{
      "Self": {"HostName":"hypervisor","TailscaleIPs":["100.64.0.1"],"Online":true},
      "Peer": {
        "nodekey:b": {"HostName":"site0-cp-101","TailscaleIPs":["100.64.0.11"],"Online":true},
        "nodekey:a": {"HostName":"site0-cp-100","TailscaleIPs":["100.64.0.10"],"Online":true}
      },
      "Health": ["could not connect to a relay"]
    }`)

	self, peers, health, err := parseStatus(raw)
	if err != nil {
		t.Fatalf("parsing a well-formed status failed: %v", err)
	}
	if self != "hypervisor" {
		t.Errorf("self = %q, want the local host's name", self)
	}
	if len(peers) != 2 {
		t.Fatalf("got %d peers, want 2 - self must not be probed, since a host "+
			"always reaches itself and a guaranteed pass makes the row mean less", len(peers))
	}
	if displayName(peers[0]) != "site0-cp-100" || displayName(peers[1]) != "site0-cp-101" {
		t.Errorf("peers are not in a stable order; two runs would not be diffable")
	}
	if len(health) != 1 {
		t.Error("the daemon's own health notes were dropped - on the night this was " +
			"written, that footnote was the only thing that admitted a fault")
	}
}

// IPv6 may be disabled at the stack on hosts in this estate, so probing a
// tailnet IPv6 address would report a broken path where a working one exists.
func TestProbeAddrPrefersIPv4(t *testing.T) {
	p := &peerStatus{TailscaleIPs: []string{"fd7a:115c:a1e0::aaaa", "100.64.0.10"}}
	if got := probeAddr(p); got != "100.64.0.10" {
		t.Errorf("probeAddr = %q, want the IPv4 address", got)
	}
	// With only IPv6 available it still has to try something rather than
	// silently skip the peer.
	only := &peerStatus{TailscaleIPs: []string{"fd7a:115c:a1e0::aaaa"}}
	if got := probeAddr(only); got == "" {
		t.Error("a peer with only an IPv6 address was skipped instead of probed")
	}
}

func TestInterpretPing(t *testing.T) {
	cases := []struct {
		name    string
		out     string
		wantOK  bool
		wantVia string
		detail  string
	}{
		{
			name:    "a direct path",
			out:     "pong from site0-cp-100 (100.64.0.10) via 10.10.10.100:41641 in 2ms",
			wantOK:  true,
			wantVia: "direct",
		},
		{
			name:    "a relayed path is still a working path",
			out:     "pong from site0-cp-100 (100.64.0.10) via DERP(ord) in 31ms",
			wantOK:  true,
			wantVia: "relay ord",
		},
		{
			name:   "the silent failure this program exists for",
			out:    "ping \"100.64.0.10\" timed out\nping \"100.64.0.10\" timed out\nno reply",
			wantOK: false,
			detail: "no reply",
		},
		{
			name:   "a peer that is not in the netmap is a different problem",
			out:    "no matching peer",
			wantOK: false,
			detail: "not in the netmap",
		},
		{
			name:   "no output at all is not success",
			out:    "",
			wantOK: false,
			detail: "no output from the probe",
		},
	}

	for _, tc := range cases {
		ok, via, detail := interpretPing(tc.out)
		if ok != tc.wantOK {
			t.Errorf("%s: reachable = %v, want %v", tc.name, ok, tc.wantOK)
		}
		if tc.wantVia != "" && via != tc.wantVia {
			t.Errorf("%s: via = %q, want %q", tc.name, via, tc.wantVia)
		}
		if tc.detail != "" && detail != tc.detail {
			t.Errorf("%s: detail = %q, want %q", tc.name, detail, tc.detail)
		}
	}
}

// A probe that cannot be read must never be scored as success. Anything
// unrecognised is a failure carrying the tool's own words, because a check
// that passes on output it did not understand is worse than no check.
func TestUnrecognisedOutputFailsRatherThanPasses(t *testing.T) {
	ok, _, detail := interpretPing("something the vendor changed\nand a second line")
	if ok {
		t.Fatal("unrecognised probe output was scored as a reachable peer")
	}
	if !strings.Contains(detail, "something the vendor changed") {
		t.Errorf("the tool's own words were dropped from the detail: %q", detail)
	}
}

// A device carrying an estate tag that nobody declared is the finding worth
// waking up for: something authenticated with an estate credential.
func TestAnUndeclaredEstateDeviceIsReported(t *testing.T) {
	peers := []*peerStatus{
		{HostName: "site0-cp-100", Tags: []string{"tag:homelab-node"}},
		{HostName: "stranger", Tags: []string{"tag:homelab-node"}},
	}
	want := Expectation{Declared: true, Members: []string{"site0-cp-100"}, TagPrefix: "tag:homelab-"}

	got := classify(peers, want)
	if len(got) != 1 || got[0].Kind != "unexpected-member" || got[0].Name != "stranger" {
		t.Fatalf("an undeclared tagged device was not reported: %+v", got)
	}
}

// A declared member that never registered is invisible in a list of peers, so
// the comparison has to run the other way too.
func TestADeclaredMemberThatNeverAppearedIsReported(t *testing.T) {
	peers := []*peerStatus{{HostName: "site0-cp-100", Tags: []string{"tag:homelab-node"}}}
	want := Expectation{
		Declared:  true,
		Members:   []string{"site0-cp-100", "site0-cp-101"},
		TagPrefix: "tag:homelab-",
	}
	got := classify(peers, want)
	if len(got) != 1 || got[0].Kind != "missing-member" || got[0].Name != "site0-cp-101" {
		t.Fatalf("a declared member that never registered was not reported: %+v", got)
	}
}

// The policy auto-approves a very wide prefix for anything wearing the router
// tag, so a device advertising a subnet nobody declared can take traffic for
// it with no human in the loop.
func TestAnUndeclaredRouteIsReported(t *testing.T) {
	peers := []*peerStatus{{
		HostName:      "hypervisor",
		Tags:          []string{"tag:homelab-router"},
		PrimaryRoutes: []string{"10.10.0.0/16", "10.99.0.0/16"},
	}}
	want := Expectation{
		Declared:  true,
		Members:   []string{"hypervisor"},
		Routes:    []string{"10.10.0.0/16"},
		TagPrefix: "tag:homelab-",
	}
	got := classify(peers, want)
	if len(got) != 1 || got[0].Kind != "unexpected-route" {
		t.Fatalf("an undeclared advertised route was not reported: %+v", got)
	}
	if !strings.Contains(got[0].Detail, "10.99.0.0/16") {
		t.Errorf("the finding does not name the route: %q", got[0].Detail)
	}
}

// Every peer carries its own address as a route. Treating those as advertised
// subnets would make every device look like a router and bury the real case.
func TestAPeersOwnAddressIsNotAnAdvertisedSubnet(t *testing.T) {
	peers := []*peerStatus{{
		HostName:      "site0-cp-100",
		Tags:          []string{"tag:homelab-node"},
		PrimaryRoutes: []string{"100.64.0.10/32", "fd7a:115c:a1e0::aaaa/128"},
	}}
	want := Expectation{Declared: true, Members: []string{"site0-cp-100"}, Routes: []string{"10.10.0.0/16"}, TagPrefix: "tag:homelab-"}
	if got := classify(peers, want); len(got) != 0 {
		t.Errorf("a peer's own address was reported as an advertised subnet: %+v", got)
	}
}

// An untagged device that somebody already accepted is not news.
func TestAKnownPersonalDeviceIsNotReported(t *testing.T) {
	peers := []*peerStatus{{HostName: "laptop"}}
	want := Expectation{Declared: true, TagPrefix: "tag:homelab-", Known: []string{"laptop"}}
	if got := classify(peers, want); len(got) != 0 {
		t.Errorf("an already-accepted personal device was reported: %+v", got)
	}
}

// The case that matters if humans are ever enrolled onto this mesh: a machine
// nobody has seen before, wearing no estate tag. Not an error - a change, and
// one worth a human looking at it the same day.
func TestANewPersonalDeviceIsReported(t *testing.T) {
	peers := []*peerStatus{
		{HostName: "laptop"},
		{HostName: "cousins-pc"},
	}
	want := Expectation{Declared: true, TagPrefix: "tag:homelab-", Known: []string{"laptop"}}

	got := classify(peers, want)
	if len(got) != 1 || got[0].Kind != "new-device" || got[0].Name != "cousins-pc" {
		t.Fatalf("a machine nobody has seen before was not reported: %+v", got)
	}
}

// Both inversions are watched, always. Neither can be switched off, because
// they cover different failures - an undeclared *tagged* device means an
// estate credential was used by something nobody built, and an unrecognised
// *untagged* device means a machine joined a mesh whose policy lets every
// device reach every other. Making either opt-in would put a blind spot
// exactly where one is least acceptable.
func TestBothInversionsAreWatchedInOneRun(t *testing.T) {
	peers := []*peerStatus{
		{HostName: "site0-cp-100", Tags: []string{"tag:homelab-node"}},
		{HostName: "stranger", Tags: []string{"tag:homelab-node"}},
		{HostName: "cousins-pc"},
	}
	want := Expectation{
		Declared:  true,
		Members:   []string{"site0-cp-100"},
		Known:     []string{"laptop"},
		TagPrefix: "tag:homelab-",
	}

	kinds := map[string]bool{}
	for _, f := range classify(peers, want) {
		kinds[f.Kind] = true
	}
	if !kinds["unexpected-member"] {
		t.Error("an undeclared estate-tagged device was not reported")
	}
	if !kinds["new-device"] {
		t.Error("an unrecognised untagged device was not reported - this is the " +
			"blind spot, and it is the one that matters if humans are ever " +
			"enrolled onto this mesh")
	}
}

// Running with nothing to compare against must not look like a clean result.
// "I was not told what belongs here" and "nothing is wrong" are different
// answers, and only one of them is true.
func TestNoExpectationIsReportedRatherThanPassing(t *testing.T) {
	peers := []*peerStatus{{HostName: "anything", Tags: []string{"tag:homelab-node"}}}

	got := classify(peers, Expectation{})
	if len(got) != 1 || got[0].Kind != "no-expectation" {
		t.Fatalf("survey fell silent instead of reporting that it could not answer: %+v", got)
	}
}

// A device wearing a tag this estate does not issue is not estate
// infrastructure, so it is judged against the baseline like any other machine
// rather than being quietly skipped for having a tag at all.
func TestAForeignTagIsNotTreatedAsEstateInfrastructure(t *testing.T) {
	peers := []*peerStatus{{HostName: "someone-elses-server", Tags: []string{"tag:not-ours"}}}
	want := Expectation{Declared: true, TagPrefix: "tag:homelab-"}

	got := classify(peers, want)
	if len(got) != 1 || got[0].Kind != "new-device" {
		t.Fatalf("a device with a foreign tag was not reported: %+v", got)
	}
}
