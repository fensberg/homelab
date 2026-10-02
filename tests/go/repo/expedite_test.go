package repo

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"homelab/details/applications"
)

// The expediter keeps each application on the build its supplier publishes,
// and its pull requests are merged without a review. Its steps are shell
// inside a workflow, and they are run here as shipped - read out of the
// workflow and executed against stand-ins for Steam and GitHub - because what
// they write is what the standing order then has to judge, and a step nobody
// ran is a pull request nobody has seen the shape of.
//
// Against applications written here: the workflow knows none by name, so what
// is proved has to hold for any.
const expediteWorkflow = "expedite.yml"

// expediteFixture is a repository with two applications that declare an
// upstream and one that does not.
func expediteFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	retry, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "retry.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for rel, body := range map[string]string{
		"scripts/retry.sh": string(retry),
		applications.Dir + "/alpha/" + applications.Declaration: `{"upstream": {"kind": "steam", "app": "111", "news": "112", "pin": "ALPHA_BUILD_VERSION"}}`,
		applications.Dir + "/alpha/" + applications.Pins:        "# what alpha is built from\nALPHA_TOOL_VERSION=1.0\nALPHA_BUILD_VERSION=500\n",
		applications.Dir + "/zeta/" + applications.Declaration:  `{"upstream": {"kind": "steam", "app": "221", "news": "222", "pin": "ZETA_SERVER_BUILD"}}`,
		applications.Dir + "/zeta/" + applications.Pins:         "ZETA_SERVER_BUILD=700\n",
		applications.Dir + "/quiet/" + applications.Declaration: `{}`,
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// The list every job works through, as procurement would print it for the
// fixture.
const expediteUpstreams = `[{"name":"alpha","app":"111","news":"112","pin":"ALPHA_BUILD_VERSION","pins":"` + applications.Dir + `/alpha/` + applications.Pins + `"},` +
	`{"name":"zeta","app":"221","news":"222","pin":"ZETA_SERVER_BUILD","pins":"` + applications.Dir + `/zeta/` + applications.Pins + `"}]`

func runExpediteStep(t *testing.T, job, step, dir, path string, env []string) (ok bool, outputs map[string]string, summary, logs string) {
	t.Helper()
	scratch := t.TempDir()
	cmd := exec.Command("bash", "-eo", "pipefail", "-c", workflowStep(t, expediteWorkflow, job, step))
	cmd.Dir = dir
	cmd.Env = append(env, "PATH="+path, "HOME="+scratch, "RUNNER_TEMP="+scratch,
		"REPO=example/homelab", "GITHUB_OUTPUT="+filepath.Join(scratch, "output"), "GITHUB_STEP_SUMMARY="+filepath.Join(scratch, "summary"))
	out, err := cmd.CombinedOutput()
	outputs = map[string]string{}
	written, _ := os.ReadFile(filepath.Join(scratch, "output"))
	for _, line := range strings.Split(string(written), "\n") {
		if k, v, found := strings.Cut(line, "="); found {
			outputs[k] = v
		}
	}
	said, _ := os.ReadFile(filepath.Join(scratch, "summary"))
	return err == nil, outputs, string(said), string(out)
}

// What the workflow works through is what procurement lists: the step is run
// as shipped, in the repository, and has to hand the later jobs one entry for
// every application that declares an upstream - each with the five things
// those jobs read from it - and an empty list when none does.
func TestTheExpediterListsWhatDeclaresAnUpstream(t *testing.T) {
	heavy(t, "runs procurement's verb through go run")
	root := repoRoot(t)
	scratch := t.TempDir()
	cmd := exec.Command("bash", "-eo", "pipefail", "-c", workflowStep(t, expediteWorkflow, "check", "List what is expedited"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GITHUB_OUTPUT="+filepath.Join(scratch, "output"), "GITHUB_STEP_SUMMARY="+filepath.Join(scratch, "summary"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the step failed: %v\n%s", err, out)
	}
	written, err := os.ReadFile(filepath.Join(scratch, "output"))
	if err != nil {
		t.Fatal(err)
	}
	raw, found := strings.CutPrefix(strings.TrimSpace(string(written)), "upstreams=")
	if !found {
		t.Fatalf("the step wrote no upstreams output: %q", written)
	}
	var listed []map[string]string
	if err := json.Unmarshal([]byte(raw), &listed); err != nil {
		t.Fatalf("the step's upstreams are not a JSON list (%v): %s", err, raw)
	}
	apps, err := applications.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]applications.Application{}
	for _, a := range apps {
		if a.Upstream != nil {
			want[a.Name] = a
		}
	}
	if len(listed) != len(want) {
		t.Errorf("%d application(s) declare an upstream and the step lists %d: %s", len(want), len(listed), raw)
	}
	for _, u := range listed {
		a, declared := want[u["name"]]
		if !declared {
			t.Errorf("the step lists %q, which declares no upstream", u["name"])
			continue
		}
		if len(u) != 5 || u["app"] != a.Upstream.App || u["news"] != a.Upstream.News || u["pin"] != a.Upstream.Pin || u["pins"] != a.PinsFile() {
			t.Errorf("%s is listed as %v, and it declares %+v with its pins in %s", a.Name, u, *a.Upstream, a.PinsFile())
		}
	}
	// And the list these tests hand the later steps is the same shape.
	var fixture []map[string]string
	if err := json.Unmarshal([]byte(expediteUpstreams), &fixture); err != nil || len(fixture) != 2 || len(fixture[0]) != 5 {
		t.Fatalf("the fixture's list is not two entries of five fields each: %v", err)
	}
	for _, field := range []string{"name", "app", "news", "pin", "pins"} {
		if fixture[0][field] == "" {
			t.Errorf("the fixture's entries have no %s, which the steps read", field)
		}
	}
}

// The doorbell is asked once per application, about the appid that
// application declares, and any of them ringing is a reason to look. The
// daily pass does not ask it at all, and an estate with nothing to expedite
// looks at nothing.
func TestTheDoorbellIsRungForEachApplication(t *testing.T) {
	// A `go` that answers for procurement's check: the announcements of one
	// appid are fresh, every other's are not, and it records what it was
	// asked.
	doorbell := func(t *testing.T, ringing string) (string, string) {
		asked := filepath.Join(t.TempDir(), "asked")
		return fakeTools(t, map[string]string{"go": `echo "$*" >> "` + asked + `"
case "$*" in
  *"expedite-check -appid ` + ringing + ` "*) echo look ;;
  *"expedite-check -appid "*) echo idle ;;
  *) echo "fake go: unexpected $*" >&2; exit 64 ;;
esac`}), asked
	}
	for name, c := range map[string]struct {
		upstreams, ringing, authoritative string
		wantLook                          string
		wantAsked                         []string
	}{
		"nothing published":              {expediteUpstreams, "none", "false", "false", []string{"-appid 112 ", "-appid 222 "}},
		"the first application's rings":  {expediteUpstreams, "112", "false", "true", []string{"-appid 112 ", "-appid 222 "}},
		"the second application's rings": {expediteUpstreams, "222", "false", "true", []string{"-appid 112 ", "-appid 222 "}},
		"the daily pass":                 {expediteUpstreams, "none", "true", "true", nil},
		"nothing to expedite":            {"[]", "112", "false", "false", nil},
		"nothing to expedite, daily":     {"[]", "112", "true", "false", nil},
	} {
		t.Run(name, func(t *testing.T) {
			path, asked := doorbell(t, c.ringing)
			ok, outputs, _, logs := runExpediteStep(t, "check", "Ask whether a supplier has published anything", t.TempDir(), path,
				[]string{"UPSTREAMS=" + c.upstreams, "AUTHORITATIVE=" + c.authoritative})
			if !ok {
				t.Fatalf("the step failed:\n%s", logs)
			}
			if outputs["look"] != c.wantLook {
				t.Errorf("look=%q, want %q", outputs["look"], c.wantLook)
			}
			said, _ := os.ReadFile(asked)
			for _, want := range c.wantAsked {
				if !strings.Contains(string(said), want) {
					t.Errorf("the doorbell was not asked about %q:\n%s", want, said)
				}
			}
			if c.wantAsked == nil && len(said) != 0 {
				t.Errorf("the doorbell was asked, and this pass does not ask it:\n%s", said)
			}
		})
	}
}

// steam is a SteamCMD that reports a public build for each appid, and the
// tools the step uses to install it.
func steam(t *testing.T, builds map[string]string) string {
	t.Helper()
	var cases strings.Builder
	for app, build := range builds {
		cases.WriteString(`  *"+app_info_print ` + app + ` "*) printf '%s\n' '"branches"' '{' '"public"' '{' '"buildid" "` + build + `"' '}' '}' ;;` + "\n")
	}
	steamcmd := "#!/bin/bash\ncase \"$* \" in\n" + cases.String() + "  *) : ;;\nesac\n"
	script := filepath.Join(t.TempDir(), "steamcmd.sh")
	if err := os.WriteFile(script, []byte(steamcmd), 0o755); err != nil {
		t.Fatal(err)
	}
	return fakeTools(t, map[string]string{
		"sudo": `exit 0`,
		"curl": `exit 0`,
		// Unpacking the download puts SteamCMD where the step looks for it.
		"tar": `dir=""; while [ $# -gt 0 ]; do if [ "$1" = "-C" ]; then dir="$2"; fi; shift; done
cp "` + script + `" "$dir/steamcmd.sh"`,
		"sleep": `exit 0`,
	})
}

// SteamCMD is asked about each application's own appid, and a delivery is
// taken for exactly those whose public build is not the one their own pins
// file records - each carrying the line to move and the file it is in.
func TestADeliveryIsTakenForEachApplicationWhoseBuildMoved(t *testing.T) {
	root := expediteFixture(t)
	ok, outputs, summary, logs := runExpediteStep(t, "deliver", "Ask SteamCMD what each build actually is", root,
		steam(t, map[string]string{"111": "500", "221": "701"}), []string{"UPSTREAMS=" + expediteUpstreams})
	if !ok {
		t.Fatalf("the step failed:\n%s", logs)
	}
	var deliveries []map[string]string
	if err := json.Unmarshal([]byte(outputs["deliveries"]), &deliveries); err != nil {
		t.Fatalf("the step's deliveries are not JSON (%v): %q", err, outputs["deliveries"])
	}
	if outputs["deliver"] != "true" || len(deliveries) != 1 {
		t.Fatalf("one application's build moved, and the step reports deliver=%s with %v\n%s", outputs["deliver"], deliveries, summary)
	}
	d := deliveries[0]
	if d["name"] != "zeta" || d["build"] != "701" || d["recorded"] != "700" || d["pin"] != "ZETA_SERVER_BUILD" || d["pins"] != applications.Dir+"/zeta/"+applications.Pins {
		t.Errorf("the delivery is %v", d)
	}

	// Nothing moved: nothing to deliver, and that is not a failure.
	ok, outputs, _, logs = runExpediteStep(t, "deliver", "Ask SteamCMD what each build actually is", root,
		steam(t, map[string]string{"111": "500", "221": "700"}), []string{"UPSTREAMS=" + expediteUpstreams})
	if !ok || outputs["deliver"] != "false" || outputs["deliveries"] != "[]" {
		t.Errorf("with every build as recorded: ok=%v, %v\n%s", ok, outputs, logs)
	}

	// Steam reporting no build for one of them is not "nothing to do".
	ok, outputs, _, logs = runExpediteStep(t, "deliver", "Ask SteamCMD what each build actually is", root,
		steam(t, map[string]string{"111": "500"}), []string{"UPSTREAMS=" + expediteUpstreams})
	if ok || outputs["deliver"] != "" || !strings.Contains(logs, "no public build for app 221") {
		t.Errorf("with no build reported for one application: ok=%v, %v\n%s", ok, outputs, logs)
	}
}

// submitted is a GitHub that serves the fixture's files and records the one
// commit the step makes.
func submitted(t *testing.T, root string) (path, dir string) {
	t.Helper()
	dir = t.TempDir()
	return fakeTools(t, map[string]string{"gh": `fake="` + dir + `"
echo "$*" >> "$fake/calls"
case "$1 $2" in
  "pr list") ;;
  "api repos/example/homelab/git/ref/heads/main") echo mainsha ;;
  "api repos/example/homelab/git/commits/mainsha") echo treesha ;;
  "api repos/example/homelab/contents/"*)
    path="${2#repos/example/homelab/contents/}"
    base64 -w0 "` + root + `/${path%%\?*}" ;;
  "api repos/example/homelab/git/blobs")
    for a in "$@"; do case "$a" in content=*) printf '%s' "${a#content=}" >> "$fake/blobs"; echo >> "$fake/blobs" ;; esac; done
    echo blobsha ;;
  "api repos/example/homelab/git/trees") echo "$*" >> "$fake/trees"; echo newtree ;;
  "api repos/example/homelab/git/commits") echo "$*" >> "$fake/commits"; echo commitsha ;;
  "api repos/example/homelab/git/refs") echo "$*" >> "$fake/refs" ;;
  "pr create") echo "$*" >> "$fake/opened"; echo https://example.invalid/pull/9 ;;
  *) echo "fake gh: unexpected $*" >&2; exit 64 ;;
esac`}), dir
}

// What the expediter submits is what its standing order covers and nothing
// else: one file, the application's own pins, with the one line the
// application declared moved to the new build and every other line as it
// was - on a branch and under a title that name the application.
func TestTheExpediterSubmitsTheDeclaredPinAndNothingElse(t *testing.T) {
	root := expediteFixture(t)
	path, fake := submitted(t, root)
	pins := applications.Dir + "/alpha/" + applications.Pins
	delivery := `[{"name":"alpha","app":"111","news":"112","pin":"ALPHA_BUILD_VERSION","pins":"` + pins + `","build":"501","recorded":"500"}]`
	ok, _, summary, logs := runExpediteStep(t, "deliver", "Submit it", root, path,
		[]string{"GH_TOKEN=x", "DELIVERIES=" + delivery, "RUN_URL=https://example.invalid/run"})
	if !ok {
		t.Fatalf("the step failed:\n%s", logs)
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(fake, name))
		if err != nil {
			return ""
		}
		return string(b)
	}
	blob, err := base64.StdEncoding.DecodeString(strings.TrimSpace(read("blobs")))
	if err != nil {
		t.Fatalf("what was committed is not one base64 file (%v): %q", err, read("blobs"))
	}
	if string(blob) != "# what alpha is built from\nALPHA_TOOL_VERSION=1.0\nALPHA_BUILD_VERSION=501\n" {
		t.Errorf("the pins file was submitted as:\n%s", blob)
	}
	if trees := read("trees"); strings.Count(trees, "tree[][path]=") != 1 || !strings.Contains(trees, "tree[][path]="+pins+" ") {
		t.Errorf("the commit changes more than the application's own pins file, or another file: %s", trees)
	}
	if refs := read("refs"); !strings.Contains(refs, "ref=refs/heads/expedite/alpha-501") {
		t.Errorf("the branch is not named for the application and the build: %s", refs)
	}
	opened := read("opened")
	if !strings.Contains(opened, "--title feat(alpha): take delivery of build 501") || !strings.Contains(opened, "--head expedite/alpha-501") {
		t.Errorf("the pull request is not one for this application and build: %s", opened)
	}
	if !regexp.MustCompile(`alpha: opened \S*9`).MatchString(summary) {
		t.Errorf("the run does not say what it opened: %q", summary)
	}

	// A pins file that has lost the line the application declares is a
	// failure, not an empty commit.
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(pins)), []byte("ALPHA_TOOL_VERSION=1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path, fake = submitted(t, root)
	ok, _, _, logs = runExpediteStep(t, "deliver", "Submit it", root, path,
		[]string{"GH_TOKEN=x", "DELIVERIES=" + delivery, "RUN_URL=https://example.invalid/run"})
	if _, err := os.Stat(filepath.Join(fake, "opened")); ok || err == nil || !strings.Contains(logs, "could not find the ALPHA_BUILD_VERSION line") {
		t.Errorf("with the declared line gone from the pins file: ok=%v\n%s", ok, logs)
	}
}
