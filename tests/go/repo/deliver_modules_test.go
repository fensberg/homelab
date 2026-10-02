package repo

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"homelab/details/pins"
)

// The workflow that brings the modules to the gate is shell around one
// procurement verb, and it is run here as shipped. The verb decides whether
// the pin moves and is proved in its own package; what is proved here is
// what the step does with the answer - that a move becomes one pull request
// changing the pins file and nothing else, and that no move becomes nothing.
const deliverModulesWorkflow = "deliver-modules.yml"

const (
	pinnedCommit = "1111111111111111111111111111111111111111"
	mergedCommit = "2222222222222222222222222222222222222222"
)

// modulesDelivery is a checkout holding a pins file, with stand-ins for git,
// GitHub and the verb that record what the step asked of them.
type modulesDelivery struct{ root, fake, path string }

func newModulesDelivery(t *testing.T, verb string) modulesDelivery {
	t.Helper()
	root, fake := t.TempDir(), t.TempDir()
	file := filepath.Join(root, filepath.FromSlash(pins.File))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("{\n  \"default\": \""+pinnedCommit+"\",\n  \"per_site\": {}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := fakeTools(t, map[string]string{
		"git": `echo "$*" >> "` + fake + `/git"`,
		// The verb, as the step runs it: -pins <file> -to <commit>.
		"go": `file=""; to=""
while [ $# -gt 0 ]; do case "$1" in -pins) file="$2"; shift ;; -to) to="$2"; shift ;; esac; shift; done
` + verb,
		"gh": `fake="` + fake + `"
echo "$*" >> "$fake/calls"
case "$1 $2" in
  "api repos/example/homelab/git/ref/heads/main") echo ` + mergedCommit + ` ;;
  "api repos/example/homelab/git/commits/` + mergedCommit + `") echo treesha ;;
  "api repos/example/homelab/git/blobs")
    for a in "$@"; do case "$a" in content=*) printf '%s\n' "${a#content=}" >> "$fake/blobs" ;; esac; done
    echo blobsha ;;
  "api repos/example/homelab/git/trees") echo "$*" >> "$fake/trees"; echo newtree ;;
  "api repos/example/homelab/git/commits") echo "$*" >> "$fake/commits"; echo commitsha ;;
  "api repos/example/homelab/git/refs") echo "$*" >> "$fake/refs" ;;
  "pr list")
    case "$*" in
      *"--head pin/"*) cat "$fake/at-the-gate" 2>/dev/null || true ;;
      *) cat "$fake/older" 2>/dev/null || true ;;
    esac ;;
  "pr create") echo "$*" >> "$fake/opened"; echo https://example.invalid/pull/50 ;;
  "pr close") echo "$*" >> "$fake/closed" ;;
  *) echo "fake gh: unexpected $*" >&2; exit 64 ;;
esac`,
	})
	return modulesDelivery{root, fake, path}
}

// The verb moving the pin, and the verb leaving it: what each prints, and
// what each does to the file it was given.
const (
	verbMoves  = `sed -i "s/` + pinnedCommit + `/$to/" "$file"; echo "moved the default pin from 1111111 to 2222222: modules/infrastructure/thing/main.tf"`
	verbStays  = `echo "nothing a site reads differs between 1111111 and 2222222, so the pin stays"`
	verbCannot = `echo "procurement deliver-modules: could not tell" >&2; exit 1`
)

func (d modulesDelivery) set(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(d.fake, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (d modulesDelivery) read(name string) string {
	b, err := os.ReadFile(filepath.Join(d.fake, name))
	if err != nil {
		return ""
	}
	return string(b)
}

func (d modulesDelivery) run(t *testing.T) (ok bool, summary, logs string) {
	t.Helper()
	scratch := t.TempDir()
	cmd := exec.Command("bash", "-eo", "pipefail", "-c", workflowStep(t, deliverModulesWorkflow, "deliver", "Deliver"))
	cmd.Dir = d.root
	cmd.Env = []string{"PATH=" + d.path, "GH_TOKEN=x", "REPO=example/homelab", "PINS=" + pins.File,
		"RUN_URL=https://example.invalid/run", "RUNNER_TEMP=" + scratch, "GITHUB_STEP_SUMMARY=" + filepath.Join(scratch, "summary")}
	out, err := cmd.CombinedOutput()
	said, _ := os.ReadFile(filepath.Join(scratch, "summary"))
	return err == nil, string(said), string(out)
}

// A move becomes one pull request: the pins file with its default moved to
// main's commit and nothing else, on a branch named for that commit, with
// main as its parent - and an older move still open is closed, because this
// commit holds everything that one did.
func TestAMoveOfThePinIsDeliveredAsOnePullRequest(t *testing.T) {
	d := newModulesDelivery(t, verbMoves)
	d.set(t, "older", "41\n")
	ok, summary, logs := d.run(t)
	if !ok {
		t.Fatalf("the step failed:\n%s", logs)
	}
	if git := d.read("git"); !strings.Contains(git, "fetch") || !strings.Contains(git, "checkout --quiet --detach "+mergedCommit) {
		t.Errorf("the modules were not read as they are on main: %s", git)
	}
	blob, err := base64.StdEncoding.DecodeString(strings.TrimSpace(d.read("blobs")))
	if err != nil || string(blob) != "{\n  \"default\": \""+mergedCommit+"\",\n  \"per_site\": {}\n}\n" {
		t.Errorf("what was committed is not the pins file with its default moved (%v):\n%s", err, blob)
	}
	if trees := d.read("trees"); strings.Count(trees, "tree[][path]=") != 1 || !strings.Contains(trees, "tree[][path]="+pins.File+" ") || !strings.Contains(trees, "base_tree=treesha") {
		t.Errorf("the commit is not main's tree with the pins file changed and nothing else: %s", trees)
	}
	if commits := d.read("commits"); !strings.Contains(commits, "parents[]="+mergedCommit) {
		t.Errorf("the commit's parent is not main: %s", commits)
	}
	if refs := d.read("refs"); !strings.Contains(refs, "ref=refs/heads/pin/"+mergedCommit) {
		t.Errorf("the branch is not named for the commit the pin moves to: %s", refs)
	}
	opened := d.read("opened")
	for _, want := range []string{"--base main", "--head pin/" + mergedCommit, "the sites run the modules as of 2222222", "modules/infrastructure/thing/main.tf", "never merged without review"} {
		if !strings.Contains(opened, want) {
			t.Errorf("the pull request does not say %q: %s", want, opened)
		}
	}
	if closed := d.read("closed"); !strings.Contains(closed, "pr close 41 ") || !strings.Contains(closed, "Superseded by 50") {
		t.Errorf("the older move still open was not closed in favour of this one: %q", closed)
	}
	if !strings.Contains(summary, "moved the default pin") || !strings.Contains(summary, "delivered as 50") {
		t.Errorf("the run does not say what it did: %q", summary)
	}
}

// No move is nothing: when the verb leaves the pin where it is, nothing is
// committed and nothing is opened, and the run says why. A move already at
// the gate is not opened twice. And a verb that could not tell fails the
// run rather than passing for "no change".
func TestNoMoveOfThePinOpensNothing(t *testing.T) {
	for name, c := range map[string]struct {
		verb, atTheGate string
		wantOK          bool
		wantSay         string
	}{
		"nothing a site reads changed": {verbStays, "", true, "so the pin stays"},
		"the move is already there":    {verbMoves, "50\n", true, "already at the gate"},
		"the verb could not tell":      {verbCannot, "", false, ""},
	} {
		t.Run(name, func(t *testing.T) {
			d := newModulesDelivery(t, c.verb)
			if c.atTheGate != "" {
				d.set(t, "at-the-gate", c.atTheGate)
			}
			ok, summary, logs := d.run(t)
			if ok != c.wantOK {
				t.Fatalf("the step's success is %v, want %v:\n%s", ok, c.wantOK, logs)
			}
			for _, wrote := range []string{"blobs", "trees", "commits", "refs", "opened", "closed"} {
				if got := d.read(wrote); got != "" {
					t.Errorf("the step wrote %s although there was nothing to deliver: %s", wrote, got)
				}
			}
			if !strings.Contains(summary, c.wantSay) {
				t.Errorf("the run does not say %q: %q", c.wantSay, summary)
			}
		})
	}
}
