package repo

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"homelab/tests/parsed"
)

// The hypervisor's network configuration has two properties that a night of
// debugging established the hard way, and neither is visible in any symptom.
// These are hermetic file assertions rather than a health check: they prove
// the playbook still *says* the right thing, which is a different question
// from whether the estate is currently well. See the epoch record.

// A marked reply must be able to reach the local routing table.
//
// The overlay marks packets its daemon originates and restores that mark onto
// every reply. VRF support requires the `local` table to be moved off priority
// 0 to 32765. The overlay's own rules land at 5210-5250, before that - so a
// marked reply for the host's own address is looked up in tables that hold no
// local route, and is discarded on the way in while the socket waits.
//
// The fix is one rule at a priority below the overlay's. This asserts it is
// still there and still ordered correctly, because a rule at the wrong
// priority is indistinguishable from no rule at all, and the failure it
// produces looks like a broken cluster rather than a routing rule.
func TestMarkedRepliesReachTheLocalTable(t *testing.T) {
	light(t)
	for _, p := range localTableRuleProblems(hypervisorCommands(t)) {
		t.Error(p)
	}
}

// overlayFirstRule is the priority of the overlay's own first rule.
const overlayFirstRule = 5210

// ipRule is an `ip rule add` a command runs: its priority, the mark it
// matches and the table it looks up.
type ipRule struct {
	pref          int
	fwmark, table string
}

// ipRuleOf is the rule a command adds, when it adds one.
func ipRuleOf(c parsed.Command) (ipRule, bool) {
	w := c.Words
	if len(w) < 3 || w[0] != "ip" || w[1] != "rule" || w[2] != "add" {
		return ipRule{}, false
	}
	r := ipRule{pref: -1}
	for i := 3; i+1 < len(w); i++ {
		switch w[i] {
		case "pref", "priority", "preference":
			if n, err := strconv.Atoi(w[i+1]); err == nil {
				r.pref = n
			}
		case "fwmark":
			r.fwmark = w[i+1]
		case "lookup", "table":
			r.table = w[i+1]
		}
	}
	return r, true
}

// overlayMark is the mark the overlay puts on what its daemon sends.
const overlayMark = "0x80000/0xff0000"

// localTableRules is each rule a set of commands adds that sends the
// overlay's marked replies to the local table.
func localTableRules(commands []parsed.Command) []ipRule {
	var out []ipRule
	for _, c := range commands {
		if r, ok := ipRuleOf(c); ok && r.fwmark == overlayMark && r.table == "local" {
			out = append(out, r)
		}
	}
	return out
}

// localTableRuleProblems is what is wrong with the rule that sends the
// overlay's marked replies to the local table: that nothing adds it, or
// that it is added where the overlay's own rules come first.
func localTableRuleProblems(commands []parsed.Command) []string {
	rules := localTableRules(commands)
	if len(rules) == 0 {
		return []string{"the playbook no longer adds a rule sending the overlay daemon's " +
			"own marked replies to the local table.\n" +
			"Without it a marked reply is routed to a table with no local route " +
			"and never reaches the socket, and the host silently leaves the overlay."}
	}
	var out []string
	for _, r := range rules {
		if r.pref < 0 || r.pref >= overlayFirstRule {
			out = append(out, fmt.Sprintf("the local-table rule is at priority %d, which is not before the "+
				"overlay's own rules at %d.\n"+
				"A marked reply is matched by those first and discarded, so this rule "+
				"never runs and the host leaves the overlay.", r.pref, overlayFirstRule))
		}
	}
	return out
}

// Every hypervisor has this collision, not just the first one.
//
// The VRF comes with the SDN and the marks come with the overlay, so any host
// running both has it. The fix must therefore be applied per host by the
// playbook rather than recorded once against one machine - which is what makes
// it survive a second hypervisor in this site, or a second site entirely.
func TestTheRoutingFixIsAppliedPerHostNotPerSite(t *testing.T) {
	light(t)
	if !restartsWithTheRule(t, hypervisorPlay(t)) {
		t.Error("the routing fix does not survive a daemon restart: no drop-in for tailscaled " +
			"adds the local-table rule when the daemon starts.\n" +
			"Applied only to the running host, it is lost on the next restart and " +
			"the host leaves the overlay with nothing reporting it.")
	}
}

// overlayDaemonDropIns is where a drop-in for the overlay's daemon is written.
const overlayDaemonDropIns = "/etc/systemd/system/tailscaled.service.d/"

// restartsWithTheRule says whether the play writes a drop-in for the
// overlay's daemon whose ExecStartPost adds the local-table rule, so that
// every start of the daemon puts it back.
func restartsWithTheRule(t *testing.T, play parsed.Play) bool {
	t.Helper()
	for _, task := range play.Tasks {
		dest, content, ok := task.Writes()
		if !ok || !strings.HasPrefix(dest, overlayDaemonDropIns) {
			continue
		}
		settings, err := parsed.UnitOf(content)
		if err != nil {
			t.Fatalf("%s writes %s, which is not a unit: %v", task.Name, dest, err)
		}
		var onStart []parsed.Setting
		for _, s := range settings {
			if s.Section == "Service" && s.Key == "ExecStartPost" {
				onStart = append(onStart, s)
			}
		}
		commands, err := parsed.UnitCommands(task.Name, onStart)
		if err != nil {
			t.Fatal(err)
		}
		if len(localTableRules(commands)) > 0 {
			return true
		}
	}
	return false
}

// IPv6 must be absent rather than half-present.
//
// A stack that is enabled with no addresses on it is worse than either end:
// clients see an IPv6-capable host, prefer it, and fail with EADDRNOTAVAIL.
// That took the overlay's control plane down for hours.
func TestIPv6IsDisabledAtTheStack(t *testing.T) {
	light(t)
	set := kernelSettings(hypervisorPlay(t))
	for _, want := range []string{"net.ipv6.conf.all.disable_ipv6", "net.ipv6.conf.default.disable_ipv6"} {
		if set[want] != "1" {
			t.Errorf("%s is not set to 1.\n"+
				"An enabled IPv6 stack with no addresses makes clients prefer a family "+
				"they cannot send from; the absence has to be honest.", want)
		}
	}
}

// kernelSettings is every kernel setting a play's sysctl tasks set, and to
// what: a task naming one, or naming {{ item }} over a loop of them.
func kernelSettings(play parsed.Play) map[string]string {
	out := map[string]string{}
	for _, task := range play.Tasks {
		if !task.Is("sysctl") {
			continue
		}
		name, _ := task.Args["name"].(string)
		value := fmt.Sprint(task.Args["value"])
		names := []string{name}
		if strings.Contains(name, "{{") {
			names = nil
			if slices.Equal(parsed.JinjaNames(name), []string{"item"}) {
				for _, item := range task.Loop {
					if s, ok := item.(string); ok {
						names = append(names, s)
					}
				}
			}
		}
		for _, n := range names {
			out[n] = value
		}
	}
	return out
}

// The rule is read from the commands that add it, whatever order its
// words come in, and judged by where it sits against the overlay's own.
func TestTheLocalTableRuleIsJudgedByThePlaceItIsAddedAt(t *testing.T) {
	light(t)
	command := func(line string) []parsed.Command {
		c, err := parsed.TemplateCommands("fixture", line)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	for name, c := range map[string]struct {
		line string
		want string
	}{
		"before the overlay":     {"ip rule add pref 5200 from all fwmark 0x80000/0xff0000 lookup local", ""},
		"words in another order": {"ip rule add fwmark 0x80000/0xff0000 table local priority 5100", ""},
		"after the overlay":      {"ip rule add pref 5300 from all fwmark 0x80000/0xff0000 lookup local", "not before the overlay's own rules"},
		"with no priority":       {"ip rule add from all fwmark 0x80000/0xff0000 lookup local", "not before the overlay's own rules"},
		"another table":          {"ip rule add pref 5200 from all fwmark 0x80000/0xff0000 lookup main", "no longer adds a rule"},
		"listed, not added":      {"ip rule list pref 5200 fwmark 0x80000/0xff0000 lookup local", "no longer adds a rule"},
	} {
		got := strings.Join(localTableRuleProblems(command(c.line)), "|")
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
}

// hypervisorPlay is the hypervisor playbook's one play.
func hypervisorPlay(t *testing.T) parsed.Play {
	t.Helper()
	plays := repository(t).Playbook(t, hypervisorPlaybook)
	if len(plays) != 1 {
		t.Fatalf("%s holds %d plays, and this reads its one", hypervisorPlaybook, len(plays))
	}
	return plays[0]
}

// hypervisorCommands is every command the hypervisor's play runs or
// installs to be run.
func hypervisorCommands(t *testing.T) []parsed.Command {
	t.Helper()
	commands, err := hypervisorPlay(t).Commands()
	if err != nil {
		t.Fatalf("%s: %v", hypervisorPlaybook, err)
	}
	return commands
}

// Signing has to be set up by the setup script, not by a chat message.
//
// The pre-push hook refuses unsigned commits, which is only reasonable if
// signing works here. It did not: the human was told three git commands in
// conversation and blocked by the hook twice in the meantime. A step a
// computer can do is not a step to ask somebody for, and an instruction that
// lives only in a reply is lost at the next context refresh.
func TestSetupConfiguresCommitSigning(t *testing.T) {
	light(t)
	setup := parsed.Commands(repository(t).Shell(t, installDependencies))
	configures := func(setting string) []parsed.Command {
		var out []parsed.Command
		for _, c := range setup {
			if len(c.Words) > 0 && c.Words[0] == "git" && slices.Contains(c.Words, "config") && slices.Contains(c.Words, setting) {
				out = append(out, c)
			}
		}
		return out
	}

	for _, want := range []string{
		"commit.gpgsign",  // the setting the hook depends on
		"gpg.format",      // ssh signing rather than gpg
		"user.signingkey", // which key
	} {
		if len(configures(want)) == 0 {
			t.Errorf("install-dependencies.sh no longer configures %s.\n\n"+
				"Without it a fresh checkout produces unsigned commits, the pre-push "+
				"hook refuses them, and the editor's sync button fails with no visible "+
				"reason - a GUI does not surface hook output.", want)
		}
	}

	// Repository-scoped. Nothing here should quietly change how somebody's
	// other checkouts behave.
	scoped := false
	for _, c := range configures("commit.gpgsign") {
		scoped = scoped || len(c.Words) > 3 && c.Words[1] == "-C" && c.Words[2] == `"$repo_root"` && c.Words[3] == "config"
	}
	if !scoped {
		t.Error("commit signing is not being set for this repository only; setup " +
			"must not reconfigure git globally on somebody's machine")
	}
}
