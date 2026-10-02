package repo

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// An App's private key, as a workflow names the secret that holds it.
var appKeySecret = regexp.MustCompile(`secrets\.([A-Z][A-Z0-9_]*_PRIVATE_KEY)\b`)

// Every App key a workflow is handed is proved by the patrol (#527).
//
// WHY THIS EXISTS. An App's key is used only when there is something to do
// with it, so a key that has stopped working is found on the day it is
// needed: procurement's was unparseable for an unknown time, and it surfaced
// the morning a game update had locked players out. The patrol now mints a
// token with each key and throws it away. That only holds for the keys it
// knows of, so this finds every one any workflow is handed - by the shape of
// the secret's name, wherever it is used - and requires the patrol's keys job
// to be handed it too. A new App is then proved from the day its key arrives.
func TestEveryAppKeyAWorkflowHoldsIsProvedByThePatrol(t *testing.T) {
	const patrol, job = "security-patrol.yml", "keys"
	proved := map[string]bool{}
	keys, ok := jobsOf(t, patrol)[job]
	if !ok {
		t.Fatalf("%s has no %q job, so no App key is proved to still work", patrol, job)
	}
	for _, s := range keys.Steps {
		for _, values := range []map[string]string{s.Env, s.With} {
			for _, v := range values {
				for _, m := range appKeySecret.FindAllStringSubmatch(v, -1) {
					proved[m[1]] = true
				}
			}
		}
	}
	for _, p := range unprovedKeys(workflowTexts(t), proved) {
		t.Error(p)
	}
	if len(proved) == 0 {
		t.Errorf("the %s job of %s is handed no App key, so it proves nothing", job, patrol)
	}
	for name, j := range jobsOf(t, patrol) {
		for scope, level := range j.Permissions {
			if level == "write" {
				t.Errorf("the patrol's %s job can write %s. It watches, and proves keys by minting tokens it throws away; it changes nothing.", name, scope)
			}
		}
	}
}

// unprovedKeys is each App key some workflow is handed that the patrol does
// not prove. A key named only in a comment is prose, and is not one.
func unprovedKeys(workflows map[string]string, proved map[string]bool) []string {
	held := map[string][]string{}
	for name, body := range workflows {
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			for _, m := range appKeySecret.FindAllStringSubmatch(line, -1) {
				if !contains(held[m[1]], name) {
					held[m[1]] = append(held[m[1]], name)
				}
			}
		}
	}
	var out []string
	for key, where := range held {
		if proved[key] {
			continue
		}
		sort.Strings(where)
		out = append(out, key+" is handed to "+strings.Join(where, ", ")+" and the patrol's keys job does not prove it still mints a token, so it can stop working unseen until the day it is needed. Add a step there that signs in with it the way its own consumer does.")
	}
	sort.Strings(out)
	return out
}

func TestUnprovedKeysFindsAKeyThePatrolDoesNotProve(t *testing.T) {
	workflows := map[string]string{
		"a.yml": "      private-key: ${{ secrets.ALPHA_BOT_PRIVATE_KEY }}\n      # secrets.ONLY_PROSE_PRIVATE_KEY is named in a comment\n",
		"b.yml": "          KEY: ${{ secrets.BETA_BOT_PRIVATE_KEY }}\n          OTHER: ${{ secrets.ALPHA_BOT_PRIVATE_KEY }}\n          NOT_ONE: ${{ secrets.SOME_TOKEN }}\n",
	}
	if got := unprovedKeys(workflows, map[string]bool{"ALPHA_BOT_PRIVATE_KEY": true, "BETA_BOT_PRIVATE_KEY": true}); len(got) != 0 {
		t.Errorf("every key proved: %v", got)
	}
	got := unprovedKeys(workflows, map[string]bool{"ALPHA_BOT_PRIVATE_KEY": true})
	if len(got) != 1 || !strings.HasPrefix(got[0], "BETA_BOT_PRIVATE_KEY is handed to b.yml") {
		t.Errorf("got %v", got)
	}
}
