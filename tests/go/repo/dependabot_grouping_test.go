package repo

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// A lockfile has exactly one writer, so Dependabot must propose one bump at a
// time per ecosystem.
//
// Ungrouped, Dependabot opens a pull request per dependency. Each one rewrites
// the same lockfile, so the first to merge leaves every sibling conflicted -
// and a conflict on a bot's branch is work it has manufactured for a person who
// did not ask for it. That happened here: @playwright/test and @types/node were
// proposed separately, the first merged, the second went red on package.json
// and pnpm-lock.yaml at once.
//
// The fix is `groups:`, and the reason it needs a test rather than a comment is
// written in the config itself - "modules/ and environments/ get their own
// entry once they exist". New ecosystem entries are expected, an entry without
// a group looks completely ordinary in a diff, and nothing fails until two
// bumps land in the same week. By then the config change is months old.
//
// So: every ecosystem entry carries a group, and that group takes the whole
// ecosystem. This is the repository-walking shape the other guards here use -
// it enumerates whatever is in the file rather than checking a list of the
// entries that existed when it was written, because the entry nobody thought
// to add to the list is precisely the one that reintroduces the race.
func TestEveryDependabotEcosystemGroupsItsUpdates(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, ".github", "dependabot.yml")

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	var cfg struct {
		Updates []struct {
			Ecosystem string `yaml:"package-ecosystem"`
			Directory string `yaml:"directory"`
			Groups    map[string]struct {
				AppliesTo   string   `yaml:"applies-to"`
				Patterns    []string `yaml:"patterns"`
				UpdateTypes []string `yaml:"update-types"`
			} `yaml:"groups"`
		} `yaml:"updates"`
	}
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	if len(cfg.Updates) == 0 {
		t.Fatalf("%s declares no ecosystems - this guard would pass vacuously", path)
	}

	for _, u := range cfg.Updates {
		where := u.Ecosystem + " in " + u.Directory

		if len(u.Groups) == 0 {
			t.Errorf("%s declares no groups: every bump becomes its own pull request, "+
				"and they will conflict with each other over the lockfile. Add a "+
				"groups: block taking patterns [\"*\"] for minor and patch.", where)
			continue
		}

		for name, g := range u.Groups {
			// Scoped to version updates on purpose. A security fix should not
			// wait for the rest of the week's bumps, so it stays ungrouped -
			// and a group that forgot to say so silently batches it.
			if g.AppliesTo != "version-updates" {
				t.Errorf("%s group %q has applies-to %q, want \"version-updates\": "+
					"without it the group also swallows security updates, which "+
					"should arrive alone and unblocked.", where, name, g.AppliesTo)
			}

			// A group that does not take "*" leaves some dependency outside it,
			// and whatever is outside races the lockfile with whatever is in.
			if len(g.Patterns) != 1 || g.Patterns[0] != "*" {
				t.Errorf("%s group %q has patterns %v, want [\"*\"]: anything the "+
					"group does not match gets its own pull request and conflicts "+
					"with the group's.", where, name, g.Patterns)
			}

			// Majors deliberately stay out, so the group must name its types
			// rather than taking everything by default.
			if len(g.UpdateTypes) == 0 {
				t.Errorf("%s group %q names no update-types: it therefore also "+
					"groups majors, and a breaking change should be read on its "+
					"own rather than bundled with patch bumps.", where, name)
				continue
			}
			for _, ut := range g.UpdateTypes {
				if ut == "major" {
					t.Errorf("%s group %q includes update-type \"major\": a "+
						"breaking change should arrive as its own pull request.",
						where, name)
				}
			}
		}
	}
}
