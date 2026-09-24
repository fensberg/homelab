package repo

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The fabricator builds every image the repository defines, and two of its
// steps decide what that means: the survey says what the products are, and the
// pin step says what versions each is built with. Both are shell inside a
// workflow, and both are run here as shipped - read out of the workflow and
// executed - because a test of a copy is a test of something that does not
// run.

const fabricatorWorkflow = "fabricator.yml"

// fabricatorStep returns the run script of the named step in the named job,
// from the workflow.
func fabricatorStep(t *testing.T, job, step string) string {
	t.Helper()
	body := workflowText(t, fabricatorWorkflow)
	if body == "" {
		t.Fatalf("%s does not exist, so nothing builds the estate's images", fabricatorWorkflow)
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(body), &wf); err != nil {
		t.Fatalf("parsing %s: %v", fabricatorWorkflow, err)
	}
	for _, s := range wf.Jobs[job].Steps {
		if s.Name == step {
			return s.Run
		}
	}
	t.Fatalf("%s has no step %q in job %q. It was renamed or removed; point this "+
		"test at what replaced it rather than deleting the test.", fabricatorWorkflow, step, job)
	return ""
}

// runFabricatorScript runs a step's script in dir with env, and returns its
// exit status and the GITHUB_OUTPUT it wrote.
func runFabricatorScript(t *testing.T, script, dir string, env []string) (ok bool, output, logs string) {
	t.Helper()
	scratch := t.TempDir()
	out := filepath.Join(scratch, "output")
	summary := filepath.Join(scratch, "summary")
	cmd := exec.Command("bash", "-eo", "pipefail", "-c", script)
	cmd.Dir = dir
	cmd.Env = append(env,
		"PATH="+os.Getenv("PATH"),
		"GITHUB_OUTPUT="+out,
		"GITHUB_STEP_SUMMARY="+summary)
	b, err := cmd.CombinedOutput()
	written, _ := os.ReadFile(out)
	return err == nil, string(written), string(b)
}

// Every Dockerfile in the repository is a product, and the survey finds each
// one under the name its image is published as.
func TestTheFabricatorSurveyFindsEveryDockerfile(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatal("jq is not on PATH; the survey uses it and this test cannot run the survey without it")
	}
	script := fabricatorStep(t, "survey", "Find every product")
	root := repoRoot(t)

	ok, output, logs := runFabricatorScript(t, script, root, nil)
	if !ok {
		t.Fatalf("the survey failed against the repository as it stands:\n\n%s", logs)
	}

	// Walked independently: every Dockerfile in the tree must appear.
	var want []string
	for _, rel := range trackedMatching(t, func(p string) bool { return filepath.Base(p) == "Dockerfile" }) {
		want = append(want, filepath.Dir(rel))
	}
	if len(want) == 0 {
		t.Fatal("no Dockerfile is tracked at all, so this checked nothing")
	}
	for _, ctx := range want {
		if !strings.Contains(output, `"context":"`+ctx+`"`) {
			t.Errorf("the survey did not list %s, so nothing would build it.\n\nIt listed:\n%s", ctx, output)
		}
	}
	for _, name := range []string{`"name":"runner"`, `"name":"valheim"`} {
		if !strings.Contains(output, name) {
			t.Errorf("the survey did not name a product %s; the published image name "+
				"is derived from it, so a wrong name publishes somewhere nothing pulls "+
				"from.\n\n%s", name, output)
		}
	}
}

// A Dockerfile the survey cannot name fails the survey rather than being
// skipped.
func TestTheFabricatorSurveyRefusesADockerfileItCannotName(t *testing.T) {
	script := fabricatorStep(t, "survey", "Find every product")
	dir := t.TempDir()
	// A product the survey can name beside the one it cannot, so skipping
	// the unnamed one would leave a survey that otherwise succeeds - which is
	// the quiet failure this is about. Alone, a skip would fail anyway for
	// having found nothing, and prove nothing.
	for _, p := range []string{"modules/applications/example/image/Dockerfile", "somewhere/else/Dockerfile"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte("FROM scratch\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ok, _, logs := runFabricatorScript(t, script, dir, nil)
	if ok {
		t.Error("the survey passed with a Dockerfile it cannot name (somewhere/else).\n\n" +
			"A product nobody builds looks exactly like one that is built, until somebody " +
			"deploys it and finds the digest never moved.")
	}
	if !strings.Contains(logs, "does not know what this Dockerfile builds") {
		t.Errorf("the survey failed, but not by refusing the unnamed Dockerfile:\n%s", logs)
	}
}

// Every Dockerfile gets every pin it asks for, at the value versions.env
// holds - the property the hand-written list in runner-image.yml got wrong,
// with a test that checked four of its five entries.
func TestTheFabricatorPassesEveryPinADockerfileAsksFor(t *testing.T) {
	script := fabricatorStep(t, "build", "Take the pins each Dockerfile asks for")
	root := repoRoot(t)
	pins := versionPins(t)

	checked := 0
	for _, rel := range trackedMatching(t, func(p string) bool { return filepath.Base(p) == "Dockerfile" }) {
		env := []string{"CONTEXT=" + filepath.Dir(rel)}
		for k, v := range pins {
			env = append(env, k+"="+v)
		}
		ok, output, logs := runFabricatorScript(t, script, root, env)
		if !ok {
			t.Errorf("%s: the pin step failed:\n\n%s", rel, logs)
			continue
		}
		for _, arg := range bareArgs(t, filepath.Join(root, rel)) {
			checked++
			want := "--build-arg " + arg + "=" + pins[arg]
			if !strings.Contains(output, want) {
				t.Errorf("%s declares ARG %s and the fabricator does not pass it as %q.\n\n"+
					"It would build with whatever the installer defaults to today.\n\n"+
					"Passed: %s", rel, arg, want, output)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no Dockerfile declares a bare ARG, so no pin was checked - either every " +
			"version moved into a default, which is a second place a version is written, " +
			"or this test is reading the wrong files")
	}
}

// A Dockerfile asking for a version nobody pinned fails the build.
func TestTheFabricatorRefusesAnUnpinnedArg(t *testing.T) {
	script := fabricatorStep(t, "build", "Take the pins each Dockerfile asks for")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "img"), 0o755); err != nil {
		t.Fatal(err)
	}
	dockerfile := "ARG UNPINNED_VERSION\nARG TARGETARCH\nFROM scratch\n"
	if err := os.WriteFile(filepath.Join(dir, "img", "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, _, logs := runFabricatorScript(t, script, dir, []string{"CONTEXT=img"})
	if ok {
		t.Error("the pin step passed a Dockerfile asking for UNPINNED_VERSION, which " +
			"versions.env does not pin - the image would build with an empty version")
	}
	if !strings.Contains(logs, "pins no UNPINNED_VERSION") {
		t.Errorf("the pin step failed, but not by naming the unpinned ARG:\n%s", logs)
	}
	if strings.Contains(logs, "pins no TARGETARCH") {
		t.Error("the pin step demanded a pin for TARGETARCH, which BuildKit fills " +
			"itself; the idiomatic bare `ARG TARGETARCH` would fail every build")
	}
}

func bareArgs(t *testing.T, dockerfile string) []string {
	t.Helper()
	f, err := os.Open(dockerfile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	automatic := map[string]bool{
		"TARGETPLATFORM": true, "TARGETOS": true, "TARGETARCH": true, "TARGETVARIANT": true,
		"BUILDPLATFORM": true, "BUILDOS": true, "BUILDARCH": true, "BUILDVARIANT": true,
	}
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && fields[0] == "ARG" && !strings.Contains(fields[1], "=") && !automatic[fields[1]] {
			out = append(out, fields[1])
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func versionPins(t *testing.T) map[string]string {
	t.Helper()
	pins := map[string]string{}
	for _, line := range strings.Split(readRepoFile(t, "scripts/versions.env"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			pins[k] = v
		}
	}
	if len(pins) == 0 {
		t.Fatal("scripts/versions.env pins nothing")
	}
	return pins
}
