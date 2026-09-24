package repo

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The fabricator builds from scripts/work-orders.json, filling each
// Dockerfile's pins from scripts/versions.env. Its two steps are shell inside
// a workflow, and both are run here as shipped - read out of the workflow and
// executed - because a test of a copy is a test of something that does not
// run. Whether the orders are complete is judged here too, by a guard, so the
// fabricator itself never has to.

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

// The fabricator builds what the work orders say and nothing else, so
// whether the orders are complete is this guard's question, not the
// fabricator's. Both directions: a Dockerfile with no order is an image
// nobody builds, which looks exactly like one that is built until somebody
// deploys it and finds the digest never moved; and an order with no
// Dockerfile is a build that fails every run.
func TestEveryDockerfileHasAWorkOrderAndEveryOrderADockerfile(t *testing.T) {
	orders := workOrders(t)
	ordered := map[string]string{}
	names := map[string]bool{}
	for _, o := range orders {
		if o.Name == "" || o.Context == "" {
			t.Errorf("a work order is missing its name or its context: %+v", o)
			continue
		}
		if names[o.Name] {
			t.Errorf("two work orders are named %q; both would publish to one image", o.Name)
		}
		names[o.Name] = true
		ordered[o.Context] = o.Name
	}

	dockerfiles := trackedMatching(t, func(p string) bool { return filepath.Base(p) == "Dockerfile" })
	if len(dockerfiles) == 0 {
		t.Fatal("no Dockerfile is tracked at all, so this checked nothing")
	}
	present := map[string]bool{}
	for _, rel := range dockerfiles {
		ctx := filepath.Dir(rel)
		present[ctx] = true
		if _, ok := ordered[ctx]; !ok {
			t.Errorf("%s has no work order in scripts/work-orders.json, so the fabricator "+
				"never builds it.\n\nAdd an order naming the image it publishes.", rel)
		}
	}
	for ctx, name := range ordered {
		if !present[ctx] {
			t.Errorf("the work order %q points at %s, which holds no Dockerfile, so its "+
				"build fails every run.", name, ctx)
		}
	}
}

// The workflow's own step hands the fabricator exactly the orders in the
// file - run as shipped, so a step that dropped or reshaped them fails here.
func TestTheFabricatorReadsEveryWorkOrder(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatal("jq is not on PATH; the step uses it and this test cannot run it without it")
	}
	script := fabricatorStep(t, "orders", "Read the work orders")
	ok, output, logs := runFabricatorScript(t, script, repoRoot(t), nil)
	if !ok {
		t.Fatalf("the step failed against the repository as it stands:\n\n%s", logs)
	}
	for _, o := range workOrders(t) {
		if !strings.Contains(output, `"name":"`+o.Name+`"`) || !strings.Contains(output, `"context":"`+o.Context+`"`) {
			t.Errorf("the work order %q (%s) did not reach the build matrix, so it is never built.\n\nThe step produced:\n%s",
				o.Name, o.Context, output)
		}
	}
}

type workOrder struct {
	Name    string `json:"name"`
	Context string `json:"context"`
}

func workOrders(t *testing.T) []workOrder {
	t.Helper()
	var f struct {
		Orders []workOrder `json:"orders"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, "scripts/work-orders.json")), &f); err != nil {
		t.Fatalf("parsing scripts/work-orders.json: %v", err)
	}
	if len(f.Orders) == 0 {
		t.Fatal("scripts/work-orders.json holds no orders, so the fabricator builds nothing")
	}
	return f.Orders
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
