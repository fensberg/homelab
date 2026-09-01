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

// Verdict is what the caller acts on.
type Verdict struct {
	From    string
	Results []result
	Health  []string
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
