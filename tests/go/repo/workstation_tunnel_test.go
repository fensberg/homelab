package repo

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"homelab/details/privatenet"
)

// lockRules runs the workstation's own script for the lock it would load.
//
// covers: shell:workstation/tunnel.sh
func lockRules(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := repoRoot(t)
	out, err := exec.Command("bash", append([]string{root + "/workstation/tunnel.sh", "rules"}, args...)...).CombinedOutput()
	return string(out), err
}

var (
	lockAccept = regexp.MustCompile(`^(?:ip daddr (\S+) )?(?:(tcp|udp) dport (\d+)|meta l4proto \{ tcp, udp \} th dport (\d+)) accept$`)
	// The house's ranges, by the names every other policy here uses for
	// them, and the ones that are this machine's own or the overlay's.
	privateSets = []string{privatenet.Ten, privatenet.OneSevenTwo, privatenet.OneNineTwo, "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16"}
)

// The workstation's tunnel connector can open this machine's SSH port and
// nothing else in the house, and that is decided on the workstation.
//
// WHY THIS EXISTS. The connector forwards wherever the account's routes send
// it, and the account is the vendor's computer: a route added by mistake, or
// by somebody who took the account, would otherwise reach the router, the
// hypervisor and the overlay through a process running inside the house. So
// the connector runs as a user of its own and a rule on the workstation holds
// that user. This reads the rule the script would load - the script itself
// proves it against the running machine, as the connector's user, every time
// it installs or checks.
//
// What the rule has to be: it applies to the connector's user and nobody
// else; the only private address it may open is this machine's, on the SSH
// port, and the resolver for names; every private range is refused before
// anything public is allowed; what is public is the vendor's edge ports; and
// what is left is refused.
func TestTheWorkstationsConnectorIsHeldToItsOwnSSHPort(t *testing.T) {
	const address, uid, resolver = "192.0.2.10", "4321", "127.0.0.53"
	body, err := lockRules(t, address, uid, resolver)
	if err != nil {
		t.Fatalf("the script printed no lock: %v\n%s", err, body)
	}
	for _, p := range lockFaults(body, address, uid, resolver) {
		t.Error(p)
	}

	// And it refuses to write a lock around something that is not one
	// machine, one user and at least one resolver.
	for name, args := range map[string][]string{
		"a range for an address": {"192.0.2.0/24", uid, resolver},
		"a name for an address":  {"workstation", uid, resolver},
		"a name for a user":      {address, "somebody", resolver},
		"no resolver":            {address, uid},
		"a resolver by name":     {address, uid, "resolver.invalid"},
	} {
		if out, err := lockRules(t, args...); err == nil {
			t.Errorf("%s: a lock was written:\n%s", name, out)
		}
	}
}

// lockFaults is each way a lock fails to hold the connector to this
// machine's SSH port.
func lockFaults(body, address, uid, resolver string) []string {
	var rules []string
	inChain := false
	for _, line := range strings.Split(body, "\n") {
		l := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(l, "type filter hook output"):
			inChain = true
			if !strings.Contains(l, "hook output") {
				return []string{"the lock is not on what this machine sends, so it holds nothing the connector opens"}
			}
		case !inChain || l == "" || strings.HasPrefix(l, "#") || l == "}":
		default:
			rules = append(rules, l)
		}
	}
	if len(rules) < 4 {
		return []string{"the lock has no rules this can read"}
	}
	var faults []string
	if rules[0] != "meta skuid != "+uid+" accept" {
		faults = append(faults, "the lock's first rule does not let everybody but the connector's user past, so it either holds the whole machine or nobody: "+rules[0])
	}
	if rules[len(rules)-1] != "reject" {
		faults = append(faults, "the lock does not end by refusing what it has not allowed: "+rules[len(rules)-1])
	}
	refused := -1
	for i, r := range rules {
		if strings.HasSuffix(r, " reject") && strings.HasPrefix(r, "ip daddr {") {
			refused = i
			for _, private := range privateSets {
				if !strings.Contains(r, private) {
					faults = append(faults, "the lock does not refuse "+private+", which is the house, the overlay or this machine itself")
				}
			}
		}
	}
	if refused < 0 {
		return append(faults, "the lock never refuses the private ranges, so the connector can open anything in the house")
	}
	for i, r := range rules[1:] {
		i++
		if r == "ct state established,related accept" || !strings.HasSuffix(r, " accept") {
			continue
		}
		m := lockAccept.FindStringSubmatch(r)
		if m == nil {
			faults = append(faults, "the lock allows something this cannot read, so what it allows is unknown: "+r)
			continue
		}
		to, proto, port := m[1], m[2], m[3]+m[4]
		switch {
		case to == address && proto == "tcp" && port == "22" && i < refused:
		case to == resolver && port == "53" && i < refused:
		case to == "" && i > refused && (port == "7844" || (proto == "tcp" && port == "443")):
		default:
			faults = append(faults, "the lock allows the connector more than this machine's SSH port, the resolver and the vendor's edge: "+r)
		}
	}
	if !strings.Contains(strings.Join(rules, "\n"), "ip daddr "+address+" tcp dport 22 accept") {
		faults = append(faults, "the lock does not allow this machine's own SSH port, so nothing arriving through the tunnel reaches it")
	}
	return faults
}

func TestLockFaultsFindsEachWayALockDoesNotHold(t *testing.T) {
	const address, uid, resolver = "192.0.2.10", "4321", "127.0.0.53"
	good := []string{
		"type filter hook output priority filter; policy accept;",
		"meta skuid != 4321 accept",
		"ct state established,related accept",
		"ip daddr 192.0.2.10 tcp dport 22 accept",
		"ip daddr 127.0.0.53 udp dport 53 accept",
		"ip daddr { 0.0.0.0/8, " + privatenet.Ten + ", 100.64.0.0/10, 127.0.0.0/8, 169.254.0.0/16, " + privatenet.OneSevenTwo + ", " + privatenet.OneNineTwo + " } reject",
		"meta l4proto { tcp, udp } th dport 7844 accept",
		"tcp dport 443 accept",
		"reject",
	}
	change := func(i int, to string) string {
		out := append([]string{}, good...)
		if to == "" {
			out = append(out[:i], out[i+1:]...)
		} else {
			out[i] = to
		}
		return strings.Join(out, "\n")
	}
	if got := lockFaults(strings.Join(good, "\n"), address, uid, resolver); len(got) != 0 {
		t.Fatalf("a lock that holds: %v", got)
	}
	for name, tc := range map[string]struct{ body, want string }{
		"it holds another user":          {change(1, "meta skuid != 0 accept"), "first rule"},
		"it holds nobody":                {change(1, ""), "first rule"},
		"the private ranges are allowed": {change(5, ""), "never refuses the private ranges"},
		"the overlay is left out":        {change(5, strings.Replace(good[5], "100.64.0.0/10, ", "", 1)), "does not refuse 100.64.0.0/10"},
		"another port of this machine":   {change(3, "ip daddr 192.0.2.10 tcp dport 8006 accept"), "more than this machine's SSH port"},
		"another address in the house":   {change(4, "ip daddr 192.0.2.1 tcp dport 22 accept"), "more than this machine's SSH port"},
		"any port out there":             {change(7, "tcp dport 25 accept"), "more than this machine's SSH port"},
		"SSH allowed after the refusal":  {strings.Join([]string{good[0], good[1], good[4], good[5], good[3], good[6], good[8]}, "\n"), "more than this machine's SSH port"},
		"its own SSH port is missing":    {change(3, ""), "does not allow this machine's own SSH port"},
		"it ends by allowing":            {change(8, "accept"), "does not end by refusing"},
		"a rule nobody can read":         {change(7, "ip daddr 203.0.113.0/24 accept"), "something this cannot read"},
	} {
		got := strings.Join(lockFaults(tc.body, address, uid, resolver), "|")
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want it to say %q", name, got, tc.want)
		}
	}
}
