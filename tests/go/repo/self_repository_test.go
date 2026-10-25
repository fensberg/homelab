package repo

import (
	"sort"
	"strings"
	"testing"
)

// Every reference to one of this repository's own actions names an action
// that is here.
//
// `uses: $/.github/...` is GitHub's reference to the repository itself.
// actionlint predates the form, reports it as a `uses:` with no ref, and is
// told to accept that one message (.github/actionlint.yaml). What goes with
// the message is the only check actionlint made of such a line: that it names
// something. So that is made here. A reference into the repository has an
// action's metadata at the path it names, and a misspelt one is found at a
// push and not when a job tries to start.
func TestEverySelfRepositoryReferenceNamesAnActionThatIsHere(t *testing.T) {
	t.Parallel()
	tracked := map[string]bool{}
	for _, rel := range trackedFiles(t) {
		tracked[rel] = true
	}

	var missing []string
	found := 0
	for _, rel := range trackedFiles(t) {
		if !runnable(rel) {
			continue
		}
		for _, uses := range scalarsAt(repository(t).YAMLDocuments(t, rel), "uses") {
			dir, ours := strings.CutPrefix(uses, "$/")
			if !ours {
				continue
			}
			found++
			if !tracked[dir+"/action.yml"] && !tracked[dir+"/action.yaml"] {
				missing = append(missing, rel+" uses $/"+dir+", and there is no action there")
			}
		}
	}
	if found == 0 {
		t.Fatal("found no reference to one of this repository's own actions, which is not this repository: the search has stopped matching")
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Error(m + ".\n\nactionlint is told to accept this form unread, so nothing else will say it names nothing until a job fails to start.")
	}
}
