package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// An exemption must not outlive the thing it was written for.
//
// .github/zizmor.yml suppresses the self-repository audit on the eight
// workflows that reach the local composite action with a relative `uses:`. The
// reason is written there at length and is a real one - actionlint refuses the
// syntax zizmor is asking for - but it is the kind of reason that expires:
// actionlint ships a release, the exemption becomes pure debt, and nothing in
// CI would ever say so. A suppressed finding is invisible by construction, so
// the only thing that can notice is a test.
//
// This is the same shape as every other ignore list here. codespell's words and
// checkov's skips each name the condition that removes them, and the point of
// writing that down is lost if nobody checks whether the condition has arrived.
//
// So: every file named must exist, and must still contain the construct the
// exemption exists for. A stale entry fails here rather than sitting in the file
// looking load-bearing.
func TestZizmorExemptionsAreStillEarned(t *testing.T) {
	root := repoRoot(t)

	var cfg struct {
		Rules map[string]struct {
			Ignore []string `yaml:"ignore"`
		} `yaml:"rules"`
	}
	raw, err := os.ReadFile(filepath.Join(root, ".github", "zizmor.yml"))
	if err != nil {
		t.Fatalf("reading .github/zizmor.yml: %v", err)
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parsing .github/zizmor.yml: %v", err)
	}

	rule, ok := cfg.Rules["self-repository"]
	if !ok {
		// Not a failure to celebrate silently: if the block has gone, the
		// relative references should have gone with it, and the sibling test
		// below is what says so.
		t.Skip("the self-repository exemption has been removed; nothing to check")
	}
	if len(rule.Ignore) == 0 {
		t.Error("the self-repository rule is present with an empty ignore list, which " +
			"suppresses nothing and reads as though it suppresses something")
	}

	for _, name := range rule.Ignore {
		// An entry may carry :line or :line:col. Only the filename is needed to
		// ask whether the exemption is still about anything.
		file := strings.SplitN(name, ":", 2)[0]
		path := filepath.Join(root, ".github", "workflows", file)

		body, err := os.ReadFile(path)
		if err != nil {
			t.Errorf(".github/zizmor.yml exempts %q from the self-repository audit, but that "+
				"workflow does not exist.\nA suppression naming a file nobody can open is "+
				"debt that looks like a decision; delete the entry.\n%v", file, err)
			continue
		}

		if !strings.Contains(string(body), "uses: ./") {
			t.Errorf(".github/zizmor.yml exempts %q from the self-repository audit, but it no "+
				"longer uses a relative `uses: ./...` reference.\nThe exemption is stale: "+
				"remove %q from the ignore list. If the list empties, remove the whole block "+
				"- the comment there says what else goes with it.", file, file)
		}
	}
}

// The other half: a workflow that reaches the local composite action and is NOT
// exempt would fail the Workflow Scan lane, which is correct but is a CI-only
// discovery. zizmor is not installed on every machine that can run `task test`,
// so this says the same thing in seconds, locally, and names the file.
//
// It is deliberately not a second copy of the audit. zizmor owns the finding;
// this owns the bookkeeping between the audit and the exemption list, which is
// the part zizmor cannot see.
func TestEveryRelativeActionReferenceIsAccountedFor(t *testing.T) {
	root := repoRoot(t)

	var cfg struct {
		Rules map[string]struct {
			Ignore []string `yaml:"ignore"`
		} `yaml:"rules"`
	}
	raw, err := os.ReadFile(filepath.Join(root, ".github", "zizmor.yml"))
	if err != nil {
		t.Fatalf("reading .github/zizmor.yml: %v", err)
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parsing .github/zizmor.yml: %v", err)
	}

	exempt := map[string]bool{}
	for _, name := range cfg.Rules["self-repository"].Ignore {
		exempt[strings.SplitN(name, ":", 2)[0]] = true
	}

	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	var checked, relative int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		checked++
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		if !strings.Contains(string(body), "uses: ./") {
			continue
		}
		relative++
		if !exempt[e.Name()] {
			t.Errorf("%s reaches a local action with `uses: ./...`, which zizmor's "+
				"self-repository audit refuses, and it is not listed in .github/zizmor.yml.\n"+
				"The Workflow Scan lane will fail on it. Read the comment in that file before "+
				"adding it: the exemption exists because actionlint rejects the syntax zizmor "+
				"wants, and if actionlint now accepts it the right fix is the other one.", e.Name())
		}
	}

	// The directory is discovered rather than listed, so a rename or a changed
	// extension would leave the loop with nothing to say and this test green on
	// an empty set. `relative` is deliberately not floored: it reaches zero
	// legitimately on the day $/ is adopted everywhere, and the sibling test
	// above is what notices the exemption list has outlived its cause.
	if checked == 0 {
		t.Fatal("found no workflows, so this test proves nothing")
	}
	t.Logf("checked %d workflows, %d of which reach a local action relatively", checked, relative)
}
