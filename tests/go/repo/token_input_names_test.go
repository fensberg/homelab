package repo

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A secret fed to a GitHub App token input is named for the value that input
// takes.
//
// The expedite duty never delivered a single update, and half of the reason
// was a name. The secret was EXPEDITE_BOT_APP_ID and it was passed as
// `client-id`, which the pinned token action documents as the App's Client
// ID - the value beginning "Iv" - with a separate `app-id` input for the
// numeric App ID. The workflow's comment said the action "accepts either";
// nothing checked, and every token mint failed with "Integration not found"
// for a week (#439). A GitHub App has an App ID, a Client ID, an installation
// ID, a slug and a bot login, and a name that could mean two of them is how the
// wrong one gets stored.
//
// So the name has to agree with the input: `client-id` takes *_CLIENT_ID, and
// `app-id` takes *_APP_ID. Read from the workflows as they will be once
// outstanding patches apply.
var tokenInput = regexp.MustCompile(`^\s*(client-id|app-id):\s*\$\{\{\s*(?:secrets|vars)\.([A-Za-z0-9_]+)\s*\}\}`)

func TestEveryAppTokenInputIsFedTheValueItsNameSays(t *testing.T) {
	workflows := intendedWorkflows(t)
	names := make([]string, 0, len(workflows))
	for name := range workflows {
		names = append(names, name)
	}
	sort.Strings(names)

	found := 0
	for _, name := range names {
		for n, line := range strings.Split(workflows[name], "\n") {
			m := tokenInput.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			found++
			input, secret := m[1], m[2]
			want := "_CLIENT_ID"
			if input == "app-id" {
				want = "_APP_ID"
			}
			if !strings.HasSuffix(secret, want) {
				t.Errorf("%s:%d passes %s to `%s`.\n\n"+
					"That input takes a value the name should say: `client-id` is the App's Client ID "+
					"(beginning \"Iv\"), and `app-id` is the numeric App ID. A secret named for one and "+
					"fed to the other is how the expedite duty failed every token mint for a week with "+
					"\"Integration not found\" (#439). Name it *%s, and store the value that name says.",
					name, n+1, secret, input, want)
			}
		}
	}
	// A floor, so a change in how the input is spelled cannot turn this into a
	// test that reads nothing and passes.
	if found == 0 {
		t.Error("no workflow passes a secret to a GitHub App token input, so this checked nothing - " +
			"the pattern has stopped matching how tokens are minted here")
	}
}
