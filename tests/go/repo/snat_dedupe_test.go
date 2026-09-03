package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The SNAT dedupe, tested against the shell the playbook actually ships.
//
// Proxmox installs the subnet's SNAT rule itself and does not check whether it
// is already there, so every SDN apply appends another identical copy - ten
// runs, ten rules (#96). The playbook collapses them back to one.
//
// This was proved once, with a stub `iptables`, in a scratch directory, and
// then thrown away. That is the "we do not hoard things locally" rule broken
// by the person who wrote it down, and it is why the logic reached the
// hypervisor with no committed evidence behind it at all.
//
// The task's own `cmd` is extracted and run rather than a copy of it being
// kept here. A copy is a second thing to keep in step, and a test that passes
// against a copy while the playbook ships something else is worse than no test
// - which is the same argument the repository already makes about two
// formatters owning one file.
const snatTaskName = "Collapse duplicate SNAT rules for the node subnet to one"

// A stub iptables backed by a text file, understanding only the two forms the
// task uses. Anything else is a loud failure rather than a silent success:
// a stub that shrugs at an unknown argument would let the task change shape
// underneath this test.
const stubIptables = `#!/bin/bash
STATE="$IPTABLES_STATE"
if [ "$1" = "-t" ] && [ "$2" = "nat" ] && [ "$3" = "-S" ]; then
  cat "$STATE"
  exit 0
fi
if [ "$1" = "-t" ] && [ "$2" = "nat" ] && [ "$3" = "-D" ]; then
  shift 3
  spec="-A $*"
  awk -v s="$spec" 'BEGIN{done=0} {if ($0==s && !done) {done=1; next} print}' "$STATE" > "$STATE.tmp"
  mv "$STATE.tmp" "$STATE"
  exit 0
fi
echo "stub iptables: unrecognised arguments: $*" >&2
exit 64
`

// shippedSnatScript pulls the task's cmd out of the playbook and substitutes
// the one Jinja variable it carries.
func shippedSnatScript(t *testing.T, subnet string) string {
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
			if name, _ := task["name"].(string); name != snatTaskName {
				continue
			}
			shell, ok := task["ansible.builtin.shell"].(map[string]any)
			if !ok {
				t.Fatalf("%q is no longer an ansible.builtin.shell task, so this test "+
					"is no longer reading what runs", snatTaskName)
			}
			cmd, ok := shell["cmd"].(string)
			if !ok {
				t.Fatalf("%q has no cmd", snatTaskName)
			}
			return strings.ReplaceAll(cmd, "{{ sdn_subnet }}", subnet)
		}
	}
	t.Fatalf("no task named %q in the playbook.\n\n"+
		"It was renamed or removed. This test exists to prove that shell collapses "+
		"duplicate rules; point it at the new name rather than deleting it.", snatTaskName)
	return ""
}

// runSnat executes the shipped script against a rule set, returning what the
// rules look like afterwards and what the task printed.
func runSnat(t *testing.T, rules []string) (after []string, stdout string) {
	t.Helper()
	const subnet = "192.0.2.0/24" // RFC 5737, never a real estate

	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "iptables"), []byte(stubIptables), 0o755); err != nil {
		t.Fatal(err)
	}

	state := filepath.Join(dir, "rules")
	if err := os.WriteFile(state, []byte(strings.Join(rules, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	script := filepath.Join(dir, "task.sh")
	if err := os.WriteFile(script, []byte(shippedSnatScript(t, subnet)), 0o755); err != nil {
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

	body, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if line != "" {
			after = append(after, line)
		}
	}
	return after, string(out)
}

const (
	ourRule   = `-A POSTROUTING -s 192.0.2.0/24 -o vmbr0 -j SNAT --to-source 192.0.2.1`
	otherRule = `-A POSTROUTING -s 198.51.100.0/24 -o vmbr0 -j MASQUERADE`
)

// Ten runs of the playbook left ten identical rules. One run of this must
// leave one.
func TestTheSnatDedupeCollapsesDuplicatesToOne(t *testing.T) {
	rules := []string{otherRule}
	for range 10 {
		rules = append(rules, ourRule)
	}

	after, out := runSnat(t, rules)

	var ours int
	for _, r := range after {
		if r == ourRule {
			ours++
		}
	}
	if ours != 1 {
		t.Errorf("got %d copies of the SNAT rule, want 1:\n%s", ours, strings.Join(after, "\n"))
	}
	if !strings.Contains(out, "removed=9") {
		t.Errorf("the task reported %q; nine of the ten should have gone", strings.TrimSpace(out))
	}
}

// Idempotent, because it runs on every playbook run. A task that removed the
// last rule would break the estate's outbound path on the second run.
func TestTheSnatDedupeLeavesASingleRuleAlone(t *testing.T) {
	after, out := runSnat(t, []string{otherRule, ourRule})

	if len(after) != 2 {
		t.Fatalf("got %d rules, want 2 (ours and the unrelated one):\n%s",
			len(after), strings.Join(after, "\n"))
	}
	if !strings.Contains(out, "removed=0") {
		t.Errorf("the task reported %q on an already-correct rule set", strings.TrimSpace(out))
	}
}

// Scoped by -s, so somebody else's NAT rule on the hypervisor is not this
// task's business. Deleting one would be a networking change nobody asked for.
func TestTheSnatDedupeLeavesUnrelatedRulesAlone(t *testing.T) {
	after, _ := runSnat(t, []string{otherRule, ourRule, ourRule})

	var found bool
	for _, r := range after {
		if r == otherRule {
			found = true
		}
	}
	if !found {
		t.Errorf("the unrelated MASQUERADE rule was removed:\n%s", strings.Join(after, "\n"))
	}
}

// A host that has never had the rule is not a failure. The task runs before
// the SDN apply has necessarily ever installed one.
func TestTheSnatDedupeIsANoOpWhenTheRuleIsAbsent(t *testing.T) {
	after, out := runSnat(t, []string{otherRule})

	if len(after) != 1 {
		t.Errorf("got %d rules, want the one unrelated rule untouched", len(after))
	}
	if !strings.Contains(out, "no rule present") {
		t.Errorf("the task reported %q; it should say the rule is absent", strings.TrimSpace(out))
	}
}
