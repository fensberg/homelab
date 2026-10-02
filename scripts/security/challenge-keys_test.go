package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// A workflow as the formatter leaves one, holding a little of everything.
const heldWorkflow = `name: Thing

# secrets.NOT_A_KEY is named in a comment, which hands nothing out.
permissions:
  contents: read

env:
  SHARED: ${{ secrets.WORKFLOW_WIDE }}

jobs:
  quiet:
    runs-on: ubuntu-latest
    steps:
      - name: Look
        run: echo look

  busy:
    runs-on: ubuntu-latest
    environment: yard
    permissions:
      contents: read
      packages: write
      issues: none
    env:
      EVERY: ${{ secrets.JOB_WIDE }}
    steps:
      - name: Fetch
        id: fetch
        env:
          GH_TOKEN: ${{ github.token }}
          OTHER: ${{ secrets.GITHUB_TOKEN }}
        run: |
          # secrets.ALSO_NOT_A_KEY
          fetch
      - uses: example/mint@v1
        id: mint

      - name: Use
        with:
          token: ${{ steps.mint.outputs.app-token }}
          key: ${{ secrets.DEPLOY_KEY }}
        uses: example/use@v1
      - name: Use
        run: echo again

  called:
    uses: ./.github/workflows/other.yml
    permissions: read-all
    secrets: inherit

  named:
    runs-on: ubuntu-latest
    environment:
      name: "quay"
    permissions: {}
    steps:
      - run: echo no name
`

// Every job and step is read with exactly what it holds: the permissions in
// force, the environment, and each secret or token where it lands.
func TestHeldByReadsWhatEveryJobAndStepHolds(t *testing.T) {
	h, err := HeldBy("thing.yml", heldWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]string{
		"thing.yml, every job":                  {"secrets.WORKFLOW_WIDE": "held"},
		"thing.yml, job `quiet`":                {"permission contents": "read"},
		"thing.yml, job `quiet`, every step":    {},
		"thing.yml, job `quiet`, step `Look`":   {},
		"thing.yml, job `busy`":                 {"permission contents": "read", "permission packages": "write", "environment yard": "held"},
		"thing.yml, job `busy`, every step":     {"secrets.JOB_WIDE": "held"},
		"thing.yml, job `busy`, step `Fetch`":   {jobToken: "held"},
		"thing.yml, job `busy`, step `mint`":    {},
		"thing.yml, job `busy`, step `Use`":     {"the token step mint minted (app-token)": "held", "secrets.DEPLOY_KEY": "held"},
		"thing.yml, job `busy`, step `Use (2)`": {},
		"thing.yml, job `called`":               {everyPermission: "read"},
		"thing.yml, job `called`, every step":   {inherited: "held"},
		"thing.yml, job `named`":                {"environment quay": "held"},
		"thing.yml, job `named`, every step":    {},
		"thing.yml, job `named`, step `step 1`": {},
	}
	for holder, keys := range want {
		got, ok := h[holder]
		if !ok {
			t.Errorf("%s was not read at all", holder)
			continue
		}
		if len(got) != len(keys) {
			t.Errorf("%s holds %v, want %v", holder, got, keys)
		}
		for k, v := range keys {
			if got[k] != v {
				t.Errorf("%s: %s is %q, want %q", holder, k, got[k], v)
			}
		}
	}
	for holder := range h {
		if _, ok := want[holder]; !ok {
			t.Errorf("read a holder that is not there: %s holding %v", holder, h[holder])
		}
	}
}

// A job with no permissions on it or on its workflow takes the repository's
// default, and that is said rather than read as holding nothing.
func TestAJobWithNoPermissionsStatedHoldsTheDefault(t *testing.T) {
	h, err := HeldBy("bare.yml", "jobs:\n  one:\n    runs-on: x\n    steps:\n      - run: echo\n")
	if err != nil {
		t.Fatal(err)
	}
	if h["bare.yml, job `one`"][defaultPermissions] == "" {
		t.Errorf("read as %v", h["bare.yml, job `one`"])
	}
}

// What cannot be read is an error, never a workflow that holds nothing.
func TestHeldByRefusesWhatItCannotRead(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"no jobs":                {"name: x\non: push\n", "no job this can read"},
		"a tab":                  {"jobs:\n  one:\n\tsteps:\n", "indented with a tab"},
		"a job with no steps":    {"jobs:\n  one:\n    runs-on: x\n", "no step this can read"},
		"a line in no job":       {"jobs:\n      stray: true\n  one:\n    steps:\n      - run: x\n", "in no job"},
		"a line in no step":      {"jobs:\n  one:\n    steps:\n        stray: true\n      - run: x\n", "in no step"},
		"permissions as a word":  {"permissions: plenty\njobs:\n  one:\n    steps:\n      - run: x\n", "is not read-all"},
		"a scope with no level":  {"jobs:\n  one:\n    permissions:\n      contents: lots\n    steps:\n      - run: x\n", "not a scope and its level"},
		"an unnamed environment": {"jobs:\n  one:\n    environment:\n      url: x\n    steps:\n      - run: x\n", "cannot read the name of"},
	} {
		if h, err := HeldBy("w.yml", tc.body); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: read as %v, %v; want an error saying %q", name, h, err, tc.want)
		}
	}
}

// Widening is listed; narrowing, and what was already held, is not.
func TestWidenedListsOnlyWhatWasNotHeldBefore(t *testing.T) {
	base := held{
		"w.yml, job `a`":              {"permission contents": "read", "permission packages": "write"},
		"w.yml, job `a`, step `Run`":  {"secrets.OLD": "held"},
		"w.yml, job `a`, step `Idle`": {},
		"w.yml, job `all`":            {everyPermission: "read"},
	}
	for name, tc := range map[string]struct {
		head held
		want []string
	}{
		"the same as the base": {base, nil},
		"a permission lowered, a secret dropped": {held{
			"w.yml, job `a`":             {"permission contents": "read", "permission packages": "read"},
			"w.yml, job `a`, step `Run`": {},
		}, nil},
		"a permission raised": {held{"w.yml, job `a`": {"permission contents": "write"}},
			[]string{"w.yml, job `a`: `contents: write` (was read)"}},
		"a permission added": {held{"w.yml, job `a`": {"permission id-token": "write"}},
			[]string{"w.yml, job `a`: `id-token: write`"}},
		"a scope that read-all already gave": {held{"w.yml, job `all`": {"permission contents": "read"}}, nil},
		"more than read-all gave": {held{"w.yml, job `all`": {"permission contents": "write"}},
			[]string{"w.yml, job `all`: `contents: write` (was read)"}},
		"every scope, raised": {held{"w.yml, job `all`": {everyPermission: "write"}},
			[]string{"w.yml, job `all`: `write-all` (was read)"}},
		"a secret reaching a step that was there": {held{"w.yml, job `a`, step `Idle`": {"secrets.NEW": "held"}},
			[]string{"w.yml, job `a`, step `Idle`: holds secrets.NEW"}},
		"a step that is new": {held{"w.yml, job `a`, step `Fresh`": {jobToken: "held"}},
			[]string{"w.yml, job `a`, step `Fresh`: holds the job's token (it is new)"}},
		"an environment": {held{"w.yml, job `a`": {"environment yard": "held"}},
			[]string{"w.yml, job `a`: runs in the environment `yard`, and so can be handed its secrets"}},
		"permissions no longer stated": {held{"w.yml, job `a`": {defaultPermissions: "held"}},
			[]string{"w.yml, job `a`: states no permissions, so its token takes the repository's default"}},
	} {
		got := Widened(base, tc.head)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("%s:\n got %q\nwant %q", name, got, tc.want)
		}
	}
}

const (
	before = "permissions:\n  contents: read\njobs:\n  plan:\n    runs-on: x\n    steps:\n      - name: Plan\n        run: plan\n"
	after  = "permissions:\n  contents: read\njobs:\n  plan:\n    runs-on: x\n    permissions:\n      contents: read\n      packages: read\n    steps:\n      - name: Plan\n        env:\n          GH_TOKEN: ${{ github.token }}\n        run: plan\n"
)

// repository is a git that holds the given workflows at each commit.
func repository(at map[string]map[string]string) Git {
	return func(args ...string) ([]byte, error) {
		switch args[0] {
		case "ls-tree":
			files, ok := at[args[4]]
			if !ok {
				return nil, errors.New("no such commit")
			}
			var out []string
			for path := range files {
				out = append(out, path)
			}
			return []byte(strings.Join(out, "\n") + "\n"), nil
		case "show":
			commit, path, _ := strings.Cut(args[1], ":")
			return []byte(at[commit][path]), nil
		}
		return nil, errors.New("not a command this repository answers")
	}
}

// The verb writes what a workflow reads: whether to ask, the digest of what
// was widened, the file to hang the conversation on, and the report. The
// digest follows what was widened and nothing else.
func TestChallengeKeysAsksAboutWhatWasWidened(t *testing.T) {
	git := repository(map[string]map[string]string{
		"base": {".github/workflows/plan.yml": before, ".github/workflows/other.yml": before, ".github/workflows/github-script/action.yml": "not a workflow"},
		"head": {".github/workflows/plan.yml": after, ".github/workflows/other.yml": before},
		// The same widening, with something unrelated changed beside it.
		"later": {".github/workflows/plan.yml": after + "      - name: More\n        run: more\n", ".github/workflows/other.yml": before},
		// And one more key handed out.
		"wider": {".github/workflows/plan.yml": after, ".github/workflows/other.yml": after},
		"empty": {},
	})
	run := func(base, head string) (code int, out, errs string) {
		var o, e bytes.Buffer
		code = challengeKeysTo([]string{"-base", base, "-head", head}, git, &o, &e)
		return code, o.String(), e.String()
	}
	value := func(out, key string) string {
		for _, l := range strings.Split(out, "\n") {
			if v, ok := strings.CutPrefix(l, key+"="); ok {
				return v
			}
		}
		return ""
	}

	code, out, _ := run("base", "head")
	if code != 0 || value(out, "asked") != "true" || value(out, "anchor") != ".github/workflows/plan.yml" {
		t.Fatalf("a widened workflow: exit %d\n%s", code, out)
	}
	for _, want := range []string{"plan.yml, job `plan`: `packages: read`", "plan.yml, job `plan`, step `Plan`: holds the job's token", "report<<" + reportDelimiter, "\n" + reportDelimiter + "\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not carry %q:\n%s", want, out)
		}
	}
	digest := value(out, "digest")
	if len(digest) != 12 {
		t.Errorf("the digest is %q", digest)
	}
	if _, later, _ := run("base", "later"); value(later, "digest") != digest {
		t.Error("a push that widened nothing further changed the digest, so an answer already given would be asked for again")
	}
	if _, wider, _ := run("base", "wider"); value(wider, "digest") == digest || value(wider, "digest") == "" {
		t.Error("a push that handed out one more key kept the digest, so the answer to the smaller question would stand for the larger")
	}

	// Narrowing, and no change, ask nothing - and still exit clean.
	for _, pair := range [][2]string{{"head", "base"}, {"head", "head"}} {
		if code, out, _ := run(pair[0], pair[1]); code != 0 || strings.TrimSpace(out) != "asked=false" {
			t.Errorf("%s to %s: exit %d, %q", pair[0], pair[1], code, out)
		}
	}

	// Not knowing is an error, not an answer.
	for name, pair := range map[string][2]string{"a commit that is not there": {"base", "gone"}, "a commit with no workflows": {"base", "empty"}, "a base that is not there": {"gone", "head"}} {
		if code, out, errs := run(pair[0], pair[1]); code != 1 || strings.Contains(out, "asked=") {
			t.Errorf("%s: exit %d, wrote %q, said %q", name, code, out, errs)
		}
	}
	var o, e bytes.Buffer
	if code := challengeKeysTo([]string{"-base", "base"}, git, &o, &e); code != 2 {
		t.Errorf("with no head: exit %d", code)
	}
	if code := challengeKeysTo([]string{"-nonsense"}, git, &o, &e); code != 2 {
		t.Errorf("with a flag that is not one: exit %d", code)
	}
}

// A workflow that cannot be read stops the whole answer.
func TestAWorkflowThatCannotBeReadIsAnError(t *testing.T) {
	git := repository(map[string]map[string]string{
		"base": {".github/workflows/plan.yml": before},
		"head": {".github/workflows/plan.yml": "name: nothing\n"},
	})
	var o, e bytes.Buffer
	if code := challengeKeysTo([]string{"-base", "base", "-head", "head"}, git, &o, &e); code != 1 || !strings.Contains(e.String(), "no job this can read") {
		t.Errorf("exit %d, said %q", code, e.String())
	}
}

// The git every run outside a test uses answers, and says why when it cannot.
func TestExecGitRunsGit(t *testing.T) {
	if out, err := execGit("--version"); err != nil || !strings.HasPrefix(string(out), "git version") {
		t.Errorf("got %q, %v", out, err)
	}
	if _, err := execGit("not-a-command"); err == nil || !strings.Contains(err.Error(), "git not-a-command") {
		t.Errorf("a command git does not have: %v", err)
	}
}

// The verb as the program runs it refuses a call with nothing to compare.
func TestChallengeKeysNeedsTwoCommits(t *testing.T) {
	if code := challengeKeys(nil); code != 2 {
		t.Errorf("exit %d", code)
	}
}
