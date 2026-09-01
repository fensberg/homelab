// Package main implements survey: does the overlay network actually carry
// traffic, measured rather than asked about.
//
// Named for a site survey. A survey measures the ground as it is rather than
// trusting the plan, which is exactly the distinction this exists to enforce.
//
// # WHY IT EXISTS
//
// The site's hypervisor left the overlay network and stayed off it for hours,
// and nothing noticed. Everything that could have noticed was looking at the
// wrong thing:
//
//   - the vendor's admin console showed the host online for the entire
//     outage, because it reports registration with the coordination server;
//   - the daemon's own service status said "active (running)" with a
//     "Connected" line, because that line is written once and goes stale;
//   - the converge-time health gate had already passed and no longer existed;
//   - the external canary correctly reported a failed job and could say
//     nothing about why.
//
// REGISTRATION IS NOT REACHABILITY. That is the whole design constraint. A
// peer holds a control-plane session, appears online, is correctly tagged, has
// an address and routes - and carries no traffic at all. Every cheap check
// reports the first condition; the question is always the second.
//
// So this never trusts a status field to mean a path exists. It reads the
// local view to learn who *should* be reachable, and then it probes each of
// them, because a probe is the only thing that can tell the two apart.
//
// # WHY IT REPORTS A ROW RATHER THAN A VERDICT
//
// The fault had a shape: every peer pair involving the broken host failed, and
// every pair not involving it succeeded. That signature localises the fault
// immediately - but only pairwise. A check that asked "can I reach the hub"
// would have said the mesh was broken and pointed at nothing.
//
// One run reports one row of the matrix: this vantage point, against every
// peer it can see. Run it on each member and the rows assemble into the whole,
// and a row of failures against an otherwise healthy matrix names the culprit
// without anybody reasoning about it. That also scales the right way - a
// second hypervisor or a second site adds rows rather than requiring anything
// here to be rewritten.
//
// # WHERE IT RUNS
//
// On a member of the overlay, which means the hypervisors rather than the
// workstation - the workstation deliberately is not one. A monitor inside the
// estate cannot report that the estate is down, which is what the external
// canary already does without holding any credential that reaches in. Peers
// measure; the canary notices when measurements stop arriving.
package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// status is the subset of the overlay CLI's JSON this needs. Deliberately
// partial: a struct that mirrors the whole document would break whenever the
// vendor adds a field, and everything below is about four of them.
type status struct {
	Self   *peerStatus            `json:"Self"`
	Peer   map[string]*peerStatus `json:"Peer"`
	Health []string               `json:"Health"`
}

type peerStatus struct {
	HostName     string   `json:"HostName"`
	DNSName      string   `json:"DNSName"`
	TailscaleIPs []string `json:"TailscaleIPs"`
	Tags         []string `json:"Tags"`

	// Online is what the coordination server believes. It is the field that
	// was true for hours while nothing could be reached, so it is recorded
	// and reported and never treated as an answer.
	Online bool `json:"Online"`

	// Relay and CurAddr describe the path the daemon *thinks* it has. Also
	// not evidence: a stale path is reported the same as a live one.
	Relay   string `json:"Relay"`
	CurAddr string `json:"CurAddr"`

	// PrimaryRoutes is what this peer is carrying traffic for. It matters
	// because the tailnet policy auto-approves a very wide prefix for any
	// device holding the router tag, so a device that should not have that
	// tag can claim a site's subnet with no human in the loop.
	PrimaryRoutes []string `json:"PrimaryRoutes"`
}

// Expectation is what the estate says should be on the overlay. Supplied by
// the caller rather than read from the vault, because this runs on a
// hypervisor which deliberately holds no vault session.
//
// Without one, survey can only report what it found. With one, it can report
// what is missing and what has no business being there - which are different
// questions from reachability and, in the second case, a different kind of
// problem entirely.
type Expectation struct {
	// Members are the device names the estate declares.
	Members []string
	// Routes are the prefixes the estate declares its routers may carry.
	Routes []string
	// TagPrefix marks a device as belonging to this estate. A device wearing
	// it that nobody declared authenticated using an estate credential.
	TagPrefix string

	// Known is the set of untagged devices somebody has already accepted -
	// laptops, phones, and anything else a human enrolled deliberately.
	//
	// This is a different mechanism from Members above, and the difference is
	// the point. Members is a *declaration*: the estate says these machines
	// should exist, and the config is the truth. Known is a *baseline*: a
	// record of what was observed and accepted last time, because there is no
	// config that can declare which humans own which laptops. Comparing
	// against a declaration finds what the estate failed to build; comparing
	// against a baseline finds what turned up on its own.
	//
	// Untagged devices are not errors. A new one is a change, and a change on
	// a mesh where the policy currently permits every device to reach every
	// other is worth a human looking at it the same day.
	Known []string

	// Declared records whether an expectation was supplied at all.
	//
	// There is deliberately no switch for turning either comparison off. Both
	// inversions matter and they matter for different reasons - an undeclared
	// *tagged* device means an estate credential was used by something nobody
	// built, and an unrecognised *untagged* device means a machine joined a
	// mesh whose policy currently lets every device reach every other. Making
	// either one opt-in would leave a blind spot exactly where a blind spot is
	// least acceptable, so the only thing this field controls is whether
	// survey knows enough to answer at all.
	//
	// When it is false, survey does not fall silent. It says that it cannot
	// answer, which is a different and much louder thing than reporting
	// nothing wrong.
	Declared bool
}

// result is one cell of the matrix: this host's view of one peer.
type result struct {
	Name string
	IP   string

	// Registered is what the console would have shown. Reachable is what a
	// probe found. The pair is the point: when they disagree, the disagreement
	// is the finding.
	Registered bool
	Reachable  bool

	// Via is how the probe got there - a direct path or a named relay - and
	// is informational rather than a pass condition. A relayed path is a
	// working path.
	Via string

	// Detail carries the probe's own words when it failed, because "no reply"
	// and "no matching peer" are different problems.
	Detail string
}

// finding is something observed that the estate did not ask for, or asked for
// and did not get. Deliberately separate from result: a peer that cannot be
// reached is an availability problem, and a peer nobody declared is not.
type finding struct {
	Kind   string // "unexpected-member", "unexpected-route", "missing-member"
	Name   string
	Detail string
}

// Verdict is what the caller acts on.
type Verdict struct {
	From     string
	Results  []result
	Health   []string
	Findings []finding
}

// unreachable returns the peers that are registered and do not answer. This is
// the condition worth failing on and the one nothing in this estate could see.
func (v Verdict) unreachable() []result {
	var out []result
	for _, r := range v.Results {
		if r.Registered && !r.Reachable {
			out = append(out, r)
		}
	}
	return out
}

// parseStatus turns the CLI's JSON into the peers worth probing.
//
// Self is excluded: a host can always reach itself and including it would put
// a guaranteed pass in every row, which is the kind of filler that makes a
// green result mean less than it appears to.
func parseStatus(raw []byte) (self string, peers []*peerStatus, health []string, err error) {
	var s status
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", nil, nil, fmt.Errorf("parsing the overlay status: %w", err)
	}
	if s.Self != nil {
		self = displayName(s.Self)
	}
	for _, p := range s.Peer {
		peers = append(peers, p)
	}
	// Stable order, so two runs are diffable and a row printed by one host can
	// be compared with a row printed by another.
	sort.Slice(peers, func(i, j int) bool {
		return displayName(peers[i]) < displayName(peers[j])
	})
	return self, peers, s.Health, nil
}

// displayName prefers the short hostname and falls back to the DNS name, so a
// peer is identifiable in a report without an address being printed. Addresses
// are printed too, but the name is what a human matches against the config.
func displayName(p *peerStatus) string {
	if p.HostName != "" {
		return p.HostName
	}
	return strings.TrimSuffix(p.DNSName, ".")
}

// probeAddr picks the address to probe. The first IPv4 address is preferred
// over the tailnet's IPv6, because a host in this estate may have IPv6
// disabled at the stack - and a probe that fails for that reason would report
// a broken path where there is a working one.
func probeAddr(p *peerStatus) string {
	for _, ip := range p.TailscaleIPs {
		if !strings.Contains(ip, ":") {
			return ip
		}
	}
	if len(p.TailscaleIPs) > 0 {
		return p.TailscaleIPs[0]
	}
	return ""
}

// interpretPing reads the overlay CLI's ping output.
//
// Parsed rather than trusted to an exit code, because the command exits zero
// in cases that are not success and the distinction between "no reply" and
// "no matching peer" is worth keeping: the first is a broken path, the second
// is a peer that is not in the netmap at all.
func interpretPing(out string) (ok bool, via string, detail string) {
	lower := strings.ToLower(out)
	switch {
	case strings.Contains(lower, "pong from"):
		via = "direct"
		if i := strings.Index(lower, "via derp"); i >= 0 {
			via = "relay"
			// Keep the region name where the CLI gives one: a relayed path
			// through an unexpected region is a hint about geography rather
			// than a fault.
			if j := strings.Index(out[i:], "("); j >= 0 {
				if k := strings.Index(out[i+j:], ")"); k > 0 {
					via = "relay " + out[i+j+1:i+j+k]
				}
			}
		} else if strings.Contains(lower, "via derp") {
			via = "relay"
		}
		return true, via, ""
	case strings.Contains(lower, "no matching peer"):
		return false, "", "not in the netmap"
	case strings.Contains(lower, "timed out"), strings.Contains(lower, "no reply"):
		return false, "", "no reply"
	case strings.TrimSpace(out) == "":
		return false, "", "no output from the probe"
	default:
		return false, "", firstLine(out)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// classify compares what is on the overlay against what the estate declared.
//
// Three questions, and only the first is about whether anything is working:
//
//   - a declared member that is not present at all - it would otherwise be
//     invisible, because a peer that never registered simply does not appear
//     in a list of peers, and a check built from that list cannot miss it;
//   - a device carrying an estate tag that nobody declared - something
//     authenticated with an estate credential;
//   - a route advertised by a device that the estate did not say may carry it.
//
// The tag is what keeps this usable. A tailnet holds laptops and phones that
// are nobody's business here; a device wearing the estate's tag is. Without a
// TagPrefix this returns nothing rather than guessing, because a check that
// reports every personal device as an intruder gets switched off within a week
// and takes the real finding with it.
func classify(peers []*peerStatus, want Expectation) []finding {
	// No expectation supplied. Report the blind spot rather than returning a
	// clean result: "I was not told what belongs here" and "nothing is wrong"
	// look identical to whoever reads the exit code, and only one of them is
	// true.
	if !want.Declared {
		return []finding{{
			Kind: "no-expectation",
			Name: "(none supplied)",
			Detail: "survey was given nothing to compare against, so it cannot say " +
				"whether any of these devices belong here",
		}}
	}

	declared := map[string]bool{}
	for _, m := range want.Members {
		declared[strings.ToLower(m)] = true
	}
	allowed := map[string]bool{}
	for _, r := range want.Routes {
		allowed[r] = true
	}
	known := map[string]bool{}
	for _, k := range want.Known {
		known[strings.ToLower(k)] = true
	}

	var out []finding
	seen := map[string]bool{}

	for _, p := range peers {
		name := displayName(p)
		lower := strings.ToLower(name)

		// A device wearing this estate's tag. Something authenticated with an
		// estate credential, so it has to be one the estate declared.
		if want.TagPrefix != "" && hasEstateTag(p, want.TagPrefix) {
			seen[lower] = true
			if !declared[lower] {
				out = append(out, finding{
					Kind: "unexpected-member",
					Name: name,
					Detail: "carries an estate tag and is not declared - something " +
						"authenticated with an estate credential",
				})
			}
		} else if !known[lower] {
			// Anything else: a laptop, a phone, a games machine, or a device
			// wearing a tag this estate does not issue. Not an error, and not
			// something a configuration can predict - which is why it is
			// compared against a baseline of what was accepted rather than
			// against a declaration of what should exist.
			out = append(out, finding{
				Kind: "new-device",
				Name: name,
				Detail: "not estate infrastructure and not in the accepted baseline - " +
					"a machine joined the mesh",
			})
		}

		// Routes are checked for every device, tagged or not. An undeclared
		// machine advertising a subnet is two findings, and the second is the
		// one that moves other people's traffic.
		for _, r := range p.PrimaryRoutes {
			if isHostRoute(r) {
				continue // a peer's own address, not a subnet it carries
			}
			if !allowed[r] {
				out = append(out, finding{
					Kind:   "unexpected-route",
					Name:   name,
					Detail: "advertises " + r + ", which the estate did not declare",
				})
			}
		}
	}

	for _, m := range want.Members {
		if !seen[strings.ToLower(m)] {
			out = append(out, finding{
				Kind:   "missing-member",
				Name:   m,
				Detail: "declared by the estate and not on the overlay at all",
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func hasEstateTag(p *peerStatus, prefix string) bool {
	for _, t := range p.Tags {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}
	return false
}

// isHostRoute reports whether a prefix is a single address rather than a
// subnet. Every peer carries its own /32 and /128; treating those as
// advertised subnets would make every device look like a router.
func isHostRoute(cidr string) bool {
	return strings.HasSuffix(cidr, "/32") || strings.HasSuffix(cidr, "/128")
}
