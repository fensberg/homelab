package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// An untrusted zone cannot reach the house LAN, or anything else private.
//
// WHAT WAS OPEN. Each untrusted zone's subnet is created with SNAT exactly like
// the internal one, and nothing anywhere separated either from 192.168.0.0/16
// (#378). So a machine in a zone meant to hold the least-trusted work on the
// estate reached the house network exactly as an ordinary worker did.
//
// NetworkPolicy does not help here and it is worth being precise about why: it
// governs PODS. The node itself runs on host networking, and the whole reason
// the zone exists is node-level compromise - container escape, then node root,
// then whatever the node can reach. The hypervisor is the only layer that can
// answer for the machine.
//
// TESTED AGAINST THE SHELL THAT SHIPS, the same way the SNAT dedupe is. The
// task's own `cmd` is pulled out of the playbook and run against a stub
// iptables; a copy kept here would be a second thing to keep in step, and a
// test passing against a copy while the playbook ships something else is worse
// than no test at all.
//
// The stub refuses arguments it does not recognise rather than shrugging, so
// the task changing shape underneath this is a loud failure.

const dmzTaskName = "Close the private estate to each untrusted zone"

// A stub iptables backed by a text file, understanding -C and -I only.
//
// -C exits non-zero when the rule is absent, which is what makes the task
// idempotent, and this has to model that exactly: a stub that always reported
// the rule present would make the task look like it does nothing, and a stub
// that always reported it absent would hide a task that inserts duplicates on
// every run - which is the defect the SNAT dedupe beside it exists to clean up.
const stubFilterIptables = `#!/bin/bash
STATE="$IPTABLES_STATE"
op="$1"; shift
rule="$*"
case "$op" in
  -C) grep -Fxq -- "$rule" "$STATE" ;;
  -I) printf '%s\n' "$rule" > "$STATE.new"; cat "$STATE" >> "$STATE.new"; mv "$STATE.new" "$STATE" ;;
  *)  echo "stub iptables: unrecognised operation: $op $rule" >&2; exit 64 ;;
esac
`

// shippedDMZScript pulls the task's cmd out of the playbook and substitutes the
// loop variables Ansible would.
func shippedDMZScript(t *testing.T, subnet, gateway string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "management", "hypervisor", "hypervisor-prep.yml"))
	if err != nil {
		t.Fatalf("reading the playbook: %v", err)
	}

	var doc []struct {
		Tasks []map[string]any `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parsing the playbook: %v", err)
	}

	for _, play := range doc {
		for _, task := range play.Tasks {
			if name, _ := task["name"].(string); name != dmzTaskName {
				continue
			}
			shell, ok := task["ansible.builtin.shell"].(map[string]any)
			if !ok {
				t.Fatalf("%q is no longer an ansible.builtin.shell task, so this test "+
					"is no longer reading what runs", dmzTaskName)
			}
			cmd, ok := shell["cmd"].(string)
			if !ok {
				t.Fatalf("%q has no cmd", dmzTaskName)
			}
			cmd = strings.ReplaceAll(cmd, "{{ item.subnet }}", subnet)
			return strings.ReplaceAll(cmd, "{{ item.gateway }}", gateway)
		}
	}
	t.Fatalf("no task named %q in the playbook.\n\n"+
		"It was renamed or removed. This test exists to prove an untrusted zone is "+
		"closed to the private estate; point it at the new name rather than deleting "+
		"it - a zone silently reaching the house LAN is the failure it guards.", dmzTaskName)
	return ""
}

// runDMZ executes the shipped script against a rule set and returns the rules
// afterwards, top first, plus what the task printed.
func runDMZ(t *testing.T, existing []string) (after []string, stdout string) {
	t.Helper()
	// RFC 5737 throughout, so no real estate address can reach this file.
	const subnet, gateway = "198.51.100.0/24", "198.51.100.1"

	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "iptables"), []byte(stubFilterIptables), 0o755); err != nil {
		t.Fatal(err)
	}

	state := filepath.Join(dir, "rules")
	body := ""
	if len(existing) > 0 {
		body = strings.Join(existing, "\n") + "\n"
	}
	if err := os.WriteFile(state, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	script := filepath.Join(dir, "task.sh")
	if err := os.WriteFile(script, []byte(shippedDMZScript(t, subnet, gateway)), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("/bin/bash", script)
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"IPTABLES_STATE="+state)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the shipped task failed: %v\n%s", err, out)
	}

	final, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(final)), "\n") {
		if line != "" {
			after = append(after, line)
		}
	}
	return after, string(out)
}

func TestAnUntrustedZoneIsClosedToThePrivateEstate(t *testing.T) {
	after, _ := runDMZ(t, nil)

	if len(after) == 0 {
		t.Fatal("the task installed no rules at all, so an untrusted zone reaches " +
			"the house LAN exactly as an ordinary worker does")
	}

	// Every private range the estate or the house could be on.
	for _, private := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		want := "FORWARD -s 198.51.100.0/24 -d " + private + " -j DROP"
		if !containsRule(after, want) {
			t.Errorf(`nothing drops traffic from the zone to %s:

  %s

The zone exists for node-level compromise. NetworkPolicy bounds its pods and
governs nothing the node does on host networking, so this is the only layer
that closes it.`, private, strings.Join(after, "\n  "))
		}
	}
}

// The zone stays usable: its own gateway and its own subnet are reachable, and
// those accepts come first.
//
// Order is the whole property and it is the half a "does the rule exist" check
// cannot see. A DROP above the gateway accept takes the machine's route and its
// DNS with it - the node comes up, joins nothing, and reports no error anybody
// is watching.
func TestAnUntrustedZoneKeepsItsGatewayAndItsOwnSubnet(t *testing.T) {
	after, _ := runDMZ(t, nil)

	gateway := indexOfRule(after, "FORWARD -s 198.51.100.0/24 -d 198.51.100.1/32 -j ACCEPT")
	ownSubnet := indexOfRule(after, "FORWARD -s 198.51.100.0/24 -d 198.51.100.0/24 -j ACCEPT")
	drop := indexOfRule(after, "FORWARD -s 198.51.100.0/24 -d 192.168.0.0/16 -j DROP")

	if gateway < 0 {
		t.Fatal("the zone's own gateway is not accepted, so the machine has no route " +
			"and no DNS - it comes up, joins nothing, and nothing reports why")
	}
	if ownSubnet < 0 {
		t.Fatal("the zone's own subnet is not accepted, so machines inside one zone " +
			"cannot reach each other. Zones are isolated from the estate, not from " +
			"themselves.")
	}
	if drop < 0 {
		t.Fatal("no drop rule was installed, so there is no ordering to check")
	}

	if gateway > drop || ownSubnet > drop {
		t.Errorf(`the accepts are below the drop:

  %s

iptables takes the first match, so a drop above the gateway accept closes the
zone's own route. Both accepts have to be reached first.`, strings.Join(after, "\n  "))
	}
}

// Running it twice changes nothing.
//
// The task inserts with -I, which always prepends. Without the -C check in
// front of it every run would add another full set - which is exactly the
// unbounded growth the SNAT dedupe beside it exists to clean up after Proxmox,
// and there would be no dedupe for these.
func TestClosingTheZoneTwiceAddsNothing(t *testing.T) {
	first, _ := runDMZ(t, nil)

	// Feed the first run's output back in, which is what a second run sees.
	second, stdout := runDMZ(t, first)

	if len(second) != len(first) {
		t.Errorf(`a second run took the rule set from %d to %d:

  %s

-I always prepends, so without the -C check in front of it every run adds
another full set and the filter table grows without bound.`,
			len(first), len(second), strings.Join(second, "\n  "))
	}
	if !strings.Contains(stdout, "added=0") {
		t.Errorf("a second run did not report added=0, so Ansible reports `changed` "+
			"on every run and nobody can tell a run that did something from one that "+
			"did not: %q", strings.TrimSpace(stdout))
	}
}

func containsRule(rules []string, want string) bool {
	return indexOfRule(rules, want) >= 0
}

func indexOfRule(rules []string, want string) int {
	for i, r := range rules {
		if strings.TrimSpace(r) == want {
			return i
		}
	}
	return -1
}
