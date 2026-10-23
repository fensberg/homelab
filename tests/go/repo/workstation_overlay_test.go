package repo

import (
	"net/url"
	"slices"
	"strings"
	"testing"

	"homelab/tests/parsed"
)

// A workstation that is not on the overlay cannot reach the estate.
//
// The hypervisor is addressed by its overlay identity, because that is the only
// address that works from inside the cluster as well as outside it (#99). A
// devbox on the LAN alone hands that address to the LAN gateway, which
// black-holes it - and a provider given an unreachable API does not fail, it
// waits. Every local `build-site` and `demolish-site` then hangs with no error at
// all. That cost a full day before anybody read a route table.
//
// So the provisioning has to put the machine on the overlay, and has to refuse
// to build one it cannot. Both halves are checked: the second is what stops an
// empty auth key producing a workstation that comes up looking healthy and
// reaches nothing, since `tailscale up --auth-key=”` exits zero having joined
// nothing.
func TestTheWorkstationIsProvisionedOntoTheOverlay(t *testing.T) {
	commands := workstationCommands(t)
	if len(tailscaleUps(commands)) == 0 {
		t.Error("workstation/provision.yml never joins the overlay.\n\n" +
			"The hypervisor is addressed by its overlay identity, so a workstation " +
			"without it cannot reach the machine it is meant to manage - and the " +
			"failure is a hang rather than an error.")
	}
	if !slices.ContainsFunc(aptSources(commands), func(a aptSource) bool { return a.host == "pkgs.tailscale.com" && a.signedBy != "" }) {
		t.Error("the overlay client is not installed from its signed apt repository.\n\n" +
			"`curl | sh` is the shape scripts/approved-suppliers.yml exists to prevent: " +
			"the package is signed, the install script is merely fetched.")
	}
}

// The router tag approves subnet routes. A workstation wearing it could claim
// a site's whole range.
func TestTheWorkstationDoesNotWearTheRouterTag(t *testing.T) {
	if slices.Contains(advertisedTags(t), "tag:homelab-router") {
		t.Error("workstation/provision.yml uses the router tag.\n\n" +
			"autoApprovers trusts that tag to advertise subnet routes, so a workstation " +
			"carrying it can claim 10.0.0.0/8 and take over a site's addressing.")
	}
}

// An empty key must stop the build, not produce a machine that cannot reach
// anything. Fail closed: "no key supplied" and "joined successfully" must not
// look the same afterwards.
func TestAWorkstationWithNoOverlayKeyIsRefused(t *testing.T) {
	if !assertsThat(workstationPlay(t), "overlay_auth_key | length > 0") {
		t.Error("nothing refuses an empty overlay_auth_key.\n\n" +
			"`tailscale up --auth-key=''` exits zero having joined nothing, so the " +
			"workstation comes up looking healthy and reaches nothing - which is the " +
			"defect this provisioning exists to prevent, reintroduced by an empty string.")
	}
}

// Whatever tag the playbook advertises has to be one the tailnet policy lets
// somebody apply, or `tailscale up` is refused at first boot on a machine
// nobody is watching.
func TestTheWorkstationTagIsDocumentedInThePolicy(t *testing.T) {
	tags := advertisedTags(t)
	if len(tags) == 0 {
		t.Fatal("the workstation advertises no tag, so there is nothing whose policy to look for")
	}
	for _, tag := range tags {
		if !mentionedIn(t, "docs/tailnet-setup.md", tag) {
			t.Errorf("the playbook advertises %s and docs/tailnet-setup.md never mentions it.\n\n"+
				"A tag nobody is permitted to apply makes `tailscale up` fail at first boot, "+
				"on a machine nobody is watching.", tag)
		}
	}
}

// workstationPlay is the workstation playbook's one play.
func workstationPlay(t *testing.T) parsed.Play {
	t.Helper()
	plays := repository(t).Playbook(t, workstationPlaybook)
	if len(plays) != 1 {
		t.Fatalf("%s holds %d plays, and this reads its one", workstationPlaybook, len(plays))
	}
	return plays[0]
}

const workstationPlaybook = "workstation/provision.yml"

// workstationCommands is every command the workstation's play runs, or
// writes into the machine it builds to be run there.
func workstationCommands(t *testing.T) []parsed.Command {
	t.Helper()
	commands, err := workstationPlay(t).Commands()
	if err != nil {
		t.Fatalf("%s: %v", workstationPlaybook, err)
	}
	return commands
}

// tailscaleUps is each `tailscale up` among the commands.
func tailscaleUps(commands []parsed.Command) []parsed.Command {
	var out []parsed.Command
	for _, c := range commands {
		if len(c.Words) >= 2 && c.Words[0] == "tailscale" && c.Words[1] == "up" {
			out = append(out, c)
		}
	}
	return out
}

// flagOf is the value a command gives a flag, as --name=value or --name
// value, and whether it gives the flag at all.
func flagOf(c parsed.Command, name string) (string, bool) {
	for i, w := range c.Words {
		if v, ok := strings.CutPrefix(w, name+"="); ok {
			return v, true
		}
		if w == name {
			if i+1 < len(c.Words) && !strings.HasPrefix(c.Words[i+1], "-") {
				return c.Words[i+1], true
			}
			return "", true
		}
	}
	return "", false
}

// advertisedTags is every tag the workstation's `tailscale up` advertises,
// each {{ variable }} read from the play's own variables.
func advertisedTags(t *testing.T) []string {
	t.Helper()
	play := workstationPlay(t)
	var out []string
	for _, c := range tailscaleUps(workstationCommands(t)) {
		v, ok := flagOf(c, "--advertise-tags")
		if !ok {
			continue
		}
		for _, tag := range strings.Split(resolve(t, play, v), ",") {
			if tag = strings.TrimSpace(tag); tag != "" {
				out = append(out, tag)
			}
		}
	}
	return out
}

// resolve is a value as the play renders it, when it is one variable of
// the play's own: {{ overlay_tag }}. Anything else is refused, because what
// it renders to cannot be read here.
func resolve(t *testing.T, play parsed.Play, v string) string {
	t.Helper()
	if !strings.Contains(v, "{{") {
		return v
	}
	names := parsed.JinjaNames(v)
	if len(names) != 1 || strings.TrimSpace(strings.Trim(strings.TrimSpace(v), "{}")) != names[0] {
		t.Fatalf("%q is worked out from more than one variable of the play's, so what it renders to cannot be read", v)
	}
	value, ok := play.Vars[names[0]].(string)
	if !ok {
		t.Fatalf("%q names %s, which the play does not set to a string", v, names[0])
	}
	return value
}

// assertsThat says whether an assert task of the play holds a condition,
// written as given, whatever its spacing.
func assertsThat(play parsed.Play, condition string) bool {
	want := strings.Join(strings.Fields(condition), " ")
	for _, task := range play.Tasks {
		if !task.Is("assert") {
			continue
		}
		conditions, _ := task.Args["that"].([]any)
		if s, ok := task.Args["that"].(string); ok {
			conditions = []any{s}
		}
		for _, c := range conditions {
			if s, ok := c.(string); ok && strings.Join(strings.Fields(s), " ") == want {
				return true
			}
		}
	}
	return false
}

// aptSource is a source line a command writes for apt: its address's host
// and the key it is signed by.
type aptSource struct{ host, signedBy string }

// aptSources is every apt source line the commands write, read from each
// word of theirs that is one: deb [options] address suite components.
func aptSources(commands []parsed.Command) []aptSource {
	var out []aptSource
	for _, c := range commands {
		for _, w := range c.Words {
			// A line that expands a variable comes back as written, in
			// its quotes.
			fields := strings.Fields(strings.Trim(w, `"`))
			if len(fields) < 3 || fields[0] != "deb" {
				continue
			}
			src := aptSource{}
			rest := fields[1:]
			if strings.HasPrefix(rest[0], "[") {
				for len(rest) > 0 {
					opt := strings.Trim(rest[0], "[]")
					if v, ok := strings.CutPrefix(opt, "signed-by="); ok {
						src.signedBy = v
					}
					done := strings.HasSuffix(rest[0], "]")
					rest = rest[1:]
					if done {
						break
					}
				}
			}
			if len(rest) == 0 {
				continue
			}
			if u, err := url.Parse(rest[0]); err == nil && u.Scheme == "https" {
				src.host = u.Host
				out = append(out, src)
			}
		}
	}
	return out
}
