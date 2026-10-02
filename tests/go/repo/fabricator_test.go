package repo

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"homelab/details/applications"
	"homelab/details/workorders"
)

// The fabricator builds from work orders - the estate's own in
// scripts/work-orders.json, and one for every application that has an image -
// filling each Dockerfile's pins from scripts/versions.env and the order's own
// pins file. Its steps are shell inside a workflow, and they are run here as
// shipped - read out of the workflow and executed - because a test of a copy
// is a test of something that does not run. Whether the orders are complete
// is judged here too, by a guard, so the fabricator itself never has to.
//
// The steps are proved against an application written here, in a repository
// written here, so that what is proved does not depend on which applications
// the estate has; and then held of every order the estate really has.

const fabricatorWorkflow = "fabricator.yml"

// fabricatorStep returns the run script of the named step in the named job,
// from the workflow.
func fabricatorStep(t *testing.T, job, step string) string {
	t.Helper()
	return workflowStep(t, fabricatorWorkflow, job, step)
}

// workflowStep returns the run script of the named step in the named job of
// a workflow, so that it can be run as shipped.
func workflowStep(t *testing.T, workflow, job, step string) string {
	t.Helper()
	body := workflowText(t, workflow)
	if body == "" {
		t.Fatalf("%s does not exist", workflow)
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
		t.Fatalf("parsing %s: %v", workflow, err)
	}
	for _, s := range wf.Jobs[job].Steps {
		if s.Name == step {
			return s.Run
		}
	}
	t.Fatalf("%s has no step %q in job %q. It was renamed or removed; point this "+
		"test at what replaced it rather than deleting the test.", workflow, step, job)
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

// fabricatorFixture is a repository with the estate's own order and three
// applications: one that is built and released, with settings for two
// environments; one that is built and not released; and one with no image.
func fabricatorFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range map[string]string{
		workorders.Path:        `{"orders":[{"name":"kit","context":"kits/kit"}]}`,
		"kits/kit/Dockerfile":  "FROM scratch\n",
		"scripts/versions.env": "ESTATE_TOOL_VERSION=1.2.3\n",

		applications.Dir + "/thing/" + applications.Declaration:   fixtureDeclaration,
		applications.Dir + "/thing/" + applications.Pins:          "# what the image is built from\nTHING_BUILD_VERSION=42\nnot_a_pin=$(touch executed)\n",
		applications.Dir + "/thing/image/Dockerfile":              "ARG THING_BUILD_VERSION\nARG ESTATE_TOOL_VERSION\nARG TARGETARCH\nFROM scratch\n",
		applications.Dir + "/thing/base/kustomization.yaml":       "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - deployment.yaml\n",
		applications.Dir + "/thing/base/deployment.yaml":          "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: thing\n  namespace: thing\nspec:\n  replicas: 1\n  selector:\n    matchLabels: {app: thing}\n  template:\n    metadata:\n      labels: {app: thing}\n    spec:\n      containers:\n        - name: thing\n          image: " + fixtureImage + "@sha256:" + strings.Repeat("0", 64) + "\n",
		applications.Dir + "/thing/production/kustomization.yaml": "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - ../base\n  - settings.yaml\n",
		applications.Dir + "/thing/production/settings.yaml":      "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n  namespace: thing\ndata:\n  level: production\n",
		applications.Dir + "/thing/staging/kustomization.yaml":    "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - ../base\n  - settings.yaml\n",
		applications.Dir + "/thing/staging/settings.yaml":         "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n  namespace: thing\ndata:\n  level: staging\n",

		applications.Dir + "/built/" + applications.Declaration:     `{}`,
		applications.Dir + "/built/image/Dockerfile":                "FROM scratch\n",
		applications.Dir + "/imageless/" + applications.Declaration: `{}`,

		// Not the application's, and so not in a release of it.
		"elsewhere/secret.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: elsewhere\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const (
	fixtureImage       = "ghcr.io/example/homelab-thing"
	fixtureDeclaration = `{"release": {"version": {"env": ["THING_NAME", "THING_KEY"], "pattern": "thing version: v\\([0-9][0-9.]*\\)", "example": {"line": "12:00:01 thing version: v2.4.1 (build 9)", "version": "2.4.1"}}}}`
)

// fixtureRelease is the release half of the fixture application's order, as
// the workflow's matrix hands it to a step.
func fixtureRelease(t *testing.T, root string) string {
	t.Helper()
	for _, o := range ordersOf(t, root) {
		if o.Name == "thing" {
			return releaseJSON(t, o)
		}
	}
	t.Fatal("the fixture has no order for its application")
	return ""
}

func releaseJSON(t *testing.T, o workorders.Order) string {
	t.Helper()
	b, err := json.Marshal(o.Release)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func ordersOf(t *testing.T, root string) []workorders.Order {
	t.Helper()
	orders, err := workorders.Read(root)
	if err != nil {
		t.Fatalf("%v, so the fabricator builds nothing", err)
	}
	return orders
}

func workOrders(t *testing.T) []workorders.Order { return ordersOf(t, repoRoot(t)) }

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
			t.Errorf("%s has no work order, so the fabricator never builds it.\n\n"+
				"An application's image is %s/<application>/image/Dockerfile, which is an order by being there; "+
				"anything else needs an order in %s naming the image it publishes.", rel, applications.Dir, workorders.Path)
		}
	}
	for ctx, name := range ordered {
		if !present[ctx] {
			t.Errorf("the work order %q points at %s, which holds no Dockerfile, so its "+
				"build fails every run.", name, ctx)
		}
	}
}

// stepOrders runs the workflow's own orders step in a repository and returns
// the matrix it hands the build.
func stepOrders(t *testing.T, root string) []workorders.Order {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatal("jq is not on PATH; the step uses it and this test cannot run it without it")
	}
	ok, output, logs := runFabricatorScript(t, fabricatorStep(t, "orders", "Read the work orders"), root, nil)
	if !ok {
		t.Fatalf("the step failed:\n\n%s", logs)
	}
	raw, found := strings.CutPrefix(strings.TrimSpace(output), "orders=")
	if !found {
		t.Fatalf("the step wrote no orders output:\n%s", output)
	}
	var got []workorders.Order
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("the step's orders are not JSON: %v\n%s", err, raw)
	}
	return got
}

// The workflow's own step hands the fabricator exactly the orders the
// programs read - the estate's own, and one for every application that has
// an image - run as shipped, so a step that dropped or reshaped one fails
// here. Against the fixture, where what the orders must be is written down;
// and against the repository, where the step and homelab/details/workorders
// must agree whatever applications there are.
func TestTheFabricatorReadsEveryWorkOrder(t *testing.T) {
	fixture := fabricatorFixture(t)
	got, err := json.Marshal(stepOrders(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	app := applications.Dir + "/thing"
	want := `[{"name":"kit","context":"kits/kit"},` +
		`{"name":"built","context":"` + applications.Dir + `/built/image","pins":"` + applications.Dir + `/built/` + applications.Pins + `"},` +
		`{"name":"thing","context":"` + app + `/image","pins":"` + app + `/` + applications.Pins + `","release":{"module":"` + app + `","version":{"env":["THING_NAME","THING_KEY"],"pattern":"thing version: v\\([0-9][0-9.]*\\)","example":{"line":"12:00:01 thing version: v2.4.1 (build 9)","version":"2.4.1"}}}}]`
	if string(got) != want {
		t.Errorf("the step's orders for the fixture are\n  %s\nwant\n  %s", got, want)
	}

	for name, root := range map[string]string{"the fixture": fixture, "the repository": repoRoot(t)} {
		step, _ := json.Marshal(stepOrders(t, root))
		read, _ := json.Marshal(ordersOf(t, root))
		if string(step) != string(read) {
			t.Errorf("%s: the workflow composes\n  %s\nand the programs read\n  %s\n\nThe fabricator would build one thing and the superintendent judge another.", name, step, read)
		}
	}
}

// pinsFor is every pin an order's build is offered: the estate's, then the
// order's own.
func pinsFor(t *testing.T, root string, o workorders.Order) map[string]string {
	t.Helper()
	pins := map[string]string{}
	files := []string{"scripts/versions.env"}
	if o.Pins != "" {
		files = append(files, o.Pins)
	}
	for _, rel := range files {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			if os.IsNotExist(err) && rel == o.Pins {
				continue // an application with nothing of its own to pin
			}
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if k, v, ok := strings.Cut(line, "="); ok {
				pins[k] = v
			}
		}
	}
	return pins
}

// estatePinsEnv is the estate's pins as the workflow's versions action
// exports them: in the environment of every later step.
func estatePinsEnv(t *testing.T, root string) []string {
	t.Helper()
	var env []string
	for k, v := range pinsFor(t, root, workorders.Order{}) {
		env = append(env, k+"="+v)
	}
	if len(env) == 0 {
		t.Fatal("scripts/versions.env pins nothing")
	}
	return env
}

// Every Dockerfile gets every pin it asks for, at the value its pins hold -
// the estate's, or the order's own - the property the hand-written list in
// runner-image.yml got wrong, with a test that checked four of its five
// entries. Held of every order in the repository and of the fixture's, where
// one pin is the application's own and one is the estate's.
func TestTheFabricatorPassesEveryPinADockerfileAsksFor(t *testing.T) {
	script := fabricatorStep(t, "build", "Take the pins each Dockerfile asks for")
	checked := 0
	for name, root := range map[string]string{"the fixture": fabricatorFixture(t), "the repository": repoRoot(t)} {
		for _, o := range ordersOf(t, root) {
			env := append(estatePinsEnv(t, root), "CONTEXT="+o.Context, "ORDER_PINS="+o.Pins)
			ok, output, logs := runFabricatorScript(t, script, root, env)
			if !ok {
				t.Errorf("%s, %s: the pin step failed:\n\n%s", name, o.Name, logs)
				continue
			}
			pins := pinsFor(t, root, o)
			for _, arg := range bareArgs(t, filepath.Join(root, filepath.FromSlash(o.Context), "Dockerfile")) {
				checked++
				want := "--build-arg " + arg + "=" + pins[arg]
				if pins[arg] == "" || !strings.Contains(output, want) {
					t.Errorf("%s: %s declares ARG %s and the fabricator does not pass it as %q.\n\n"+
						"It would build with whatever the installer defaults to today.\n\n"+
						"Passed: %s", name, o.Context, arg, want, output)
				}
			}
		}
	}
	if checked < 2 {
		t.Fatal("fewer than the fixture's two pins were checked, so this is reading the wrong files")
	}
}

// An order's pins file is read, never run: a line that is not NAME=value is
// passed over, and nothing in it is executed.
func TestTheFabricatorReadsAnOrdersPinsAndRunsNothingInThem(t *testing.T) {
	root := fabricatorFixture(t)
	script := fabricatorStep(t, "build", "Take the pins each Dockerfile asks for")
	app := applications.Dir + "/thing"
	ok, output, logs := runFabricatorScript(t, script, root, []string{"ESTATE_TOOL_VERSION=1.2.3", "CONTEXT=" + app + "/image", "ORDER_PINS=" + app + "/" + applications.Pins})
	if !ok {
		t.Fatalf("the pin step failed:\n%s", logs)
	}
	if !strings.Contains(output, "--build-arg THING_BUILD_VERSION=42") || !strings.Contains(output, "--build-arg ESTATE_TOOL_VERSION=1.2.3") {
		t.Errorf("the application's own pin and the estate's were not both passed: %q", output)
	}
	if _, err := os.Stat(filepath.Join(root, "executed")); err == nil {
		t.Error("a line of the pins file was executed; the file is data")
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
	ok, _, logs := runFabricatorScript(t, script, dir, []string{"CONTEXT=img", "ORDER_PINS=img/absent.env"})
	if ok {
		t.Error("the pin step passed a Dockerfile asking for UNPINNED_VERSION, which " +
			"nothing pins - the image would build with an empty version")
	}
	if !strings.Contains(logs, "pins UNPINNED_VERSION") {
		t.Errorf("the pin step failed, but not by naming the unpinned ARG:\n%s", logs)
	}
	if strings.Contains(logs, "pins TARGETARCH") {
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

// --- the release steps -------------------------------------------------------

// fakeTools puts scripts named for each tool on a PATH ahead of the real one,
// so a release step can be run as shipped without a registry or a daemon.
func fakeTools(t *testing.T, tools map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range tools {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/bash\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir + string(os.PathListSeparator) + os.Getenv("PATH")
}

func runReleaseStep(t *testing.T, step, dir, path string, env []string) (ok bool, output, logs string) {
	t.Helper()
	script := fabricatorStep(t, "build", step)
	scratch := t.TempDir()
	out := filepath.Join(scratch, "output")
	cmd := exec.Command("bash", "-eo", "pipefail", "-c", script)
	cmd.Dir = dir
	cmd.Env = append(env, "PATH="+path, "GITHUB_OUTPUT="+out,
		"GITHUB_STEP_SUMMARY="+filepath.Join(scratch, "summary"), "RUNNER_TEMP="+scratch)
	b, err := cmd.CombinedOutput()
	written, readErr := os.ReadFile(out)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("reading the step's output: %v", readErr)
	}
	return err == nil, string(written), string(b)
}

// printing is a docker that starts a container which prints the given lines.
func printing(t *testing.T, lines ...string) string {
	t.Helper()
	logs := filepath.Join(t.TempDir(), "logs")
	if err := os.WriteFile(logs, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return fakeTools(t, map[string]string{"docker": `case "$1" in
  run) echo probe-id ;;
  logs) cat "` + logs + `" ;;
  rm) ;;
esac`})
}

// The version is what the running software says. Every order that makes a
// release gives a line the software really prints and the version to read
// from it, and the workflow's own step is run against that line among others:
// a pattern that reads nothing, or reads something else, is refused here and
// not by the first build that needs it. Held of the fixture, so the step is
// proved whichever applications the estate has, and of every order it has.
func TestTheReleaseIsNamedForTheVersionTheSoftwareReports(t *testing.T) {
	checked := 0
	for name, root := range map[string]string{"the fixture": fabricatorFixture(t), "the repository": repoRoot(t)} {
		for _, o := range ordersOf(t, root) {
			if o.Release == nil {
				continue
			}
			checked++
			example := o.Release.Version.Example
			path := printing(t, "Initialize engine version: 6000.0.75f1", "    Version:  NULL 1.0 [1.0]", example.Line, "ready")
			ok, output, logs := runReleaseStep(t, "Read the version the build carries", t.TempDir(), path, []string{
				"IMAGE=ghcr.io/example/homelab-" + o.Name, "DIGEST=sha256:" + strings.Repeat("a", 64),
				"RELEASE=" + releaseJSON(t, o), "PROBE_ATTEMPTS=2", "PROBE_INTERVAL=0"})
			if !ok {
				t.Errorf("%s, %s: the version step failed on the line the order says the software prints:\n%s", name, o.Name, logs)
				continue
			}
			if !strings.Contains(output, "version="+example.Version+"\n") {
				t.Errorf("%s, %s: want version=%s from %q, got %q", name, o.Name, example.Version, example.Line, output)
			}
		}
	}
	if checked == 0 {
		t.Fatal("not even the fixture's release was checked")
	}
}

// Software that never prints its version fails the build rather than
// releasing under a guess.
func TestAReleaseIsRefusedWhenTheVersionCannotBeRead(t *testing.T) {
	path := printing(t, "Initialize engine version: 6000.0.75f1")
	ok, output, logs := runReleaseStep(t, "Read the version the build carries", t.TempDir(), path, []string{
		"IMAGE=" + fixtureImage, "DIGEST=sha256:" + strings.Repeat("a", 64),
		"RELEASE=" + fixtureRelease(t, fabricatorFixture(t)), "PROBE_ATTEMPTS=2", "PROBE_INTERVAL=0"})
	if ok || strings.Contains(output, "version=") {
		t.Errorf("the step passed, or named a version, for software that never printed one:\n%s%s", output, logs)
	}
	if !strings.Contains(logs, "no version to release it as") {
		t.Errorf("the step failed, but not by saying the version could not be read:\n%s", logs)
	}
}

// The counter continues from the highest release of the same version, and a
// package that does not exist yet is the first release.
func TestTheReleaseCounterFollowsTheRegistry(t *testing.T) {
	for name, tc := range map[string]struct{ versions, want string }{
		"continues after the highest": {`echo 1.0.15-1; echo 1.0.15-3; echo 1.0.14-9; echo latest`, "tag=1.0.15-4\n"},
		"no package yet":              {`echo "gh: Not Found (HTTP 404)" >&2; exit 1`, "tag=1.0.15-1\n"},
		"a new version starts again":  {`echo 1.0.14-9`, "tag=1.0.15-1\n"},
	} {
		t.Run(name, func(t *testing.T) {
			// gh api --paginate <path>: the path is the third argument.
			path := fakeTools(t, map[string]string{"gh": `if [ "$2" = "--paginate" ]; then set -- "$1" "$3"; fi
case "$2" in
  users/*) echo Organization ;;
  *) ` + tc.versions + ` ;;
esac`})
			ok, output, logs := runReleaseStep(t, "Number the release", t.TempDir(), path, []string{
				"OWNER=example", "PACKAGE=homelab-thing-release", "VERSION=1.0.15", "GH_TOKEN=x"})
			if !ok {
				t.Fatalf("the step failed:\n%s", logs)
			}
			if output != tc.want {
				t.Errorf("want %q, got %q", tc.want, output)
			}
		})
	}
}

// A registry error that is not "no such package" stops the release: numbering
// from an empty list would publish a counter that already exists.
func TestTheReleaseCounterRefusesARegistryItCannotRead(t *testing.T) {
	path := fakeTools(t, map[string]string{"gh": `if [ "$2" = "--paginate" ]; then echo "gh: Server Error (HTTP 500)" >&2; exit 1; fi
echo Organization`})
	ok, output, logs := runReleaseStep(t, "Number the release", t.TempDir(), path, []string{
		"OWNER=example", "PACKAGE=homelab-thing-release", "VERSION=1.0.15", "GH_TOKEN=x"})
	if ok || output != "" {
		t.Errorf("the step numbered a release from a registry it could not read: %q\n%s", output, logs)
	}
}

// The release carries the application's own directory and nothing else, with
// the new image pinned by digest in its base - and that is what is pushed.
func TestTheReleaseCarriesTheApplicationAloneWithTheNewDigest(t *testing.T) {
	root := fabricatorFixture(t)
	scratch := t.TempDir()
	path := fakeTools(t, map[string]string{"flux": `if [ "$1" = tag ]; then echo "$@" > "` + scratch + `/tagged"; exit 0; fi
path=""
for a in "$@"; do case "$a" in --path=*) path="${a#--path=}" ;; esac; done
cp -R "$path" "` + scratch + `/pushed"
echo '{"repository":"` + fixtureImage + `-release","tag":"1.0.15-1","digest":"sha256:` + strings.Repeat("b", 64) + `"}'`})
	digest := "sha256:" + strings.Repeat("a", 64)
	ok, output, logs := runReleaseStep(t, "Stage and publish the release", root, path, []string{
		"IMAGE=" + fixtureImage, "DIGEST=" + digest,
		"ARTIFACT=" + fixtureImage + "-release", "TAG=1.0.15-1",
		"RELEASE=" + fixtureRelease(t, root), "SHA=0123456789abcdef", "SOURCE=https://github.com/example/homelab",
		"FINGERPRINT=fp-0123456789abcdef"})
	if !ok {
		t.Fatalf("the release step failed:\n%s", logs)
	}
	tagged, _ := os.ReadFile(filepath.Join(scratch, "tagged"))
	if !strings.Contains(string(tagged), "1.0.15-1") || !strings.Contains(string(tagged), "--tag fp-0123456789abcdef") {
		t.Errorf("the published release was not tagged with its fingerprint, so the next build of the "+
			"same manifests would publish it again (#514): %q", tagged)
	}
	if !strings.Contains(output, "digest=sha256:"+strings.Repeat("b", 64)) {
		t.Errorf("the step did not report the pushed artifact's digest: %q", output)
	}

	// Everything pushed is under the application's own directory, at the
	// path it has in the repository - which is the path a site's block
	// reconciles.
	app := filepath.FromSlash(applications.Dir + "/thing")
	var pushed, outside []string
	err := filepath.WalkDir(filepath.Join(scratch, "pushed"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(filepath.Join(scratch, "pushed"), p)
		if err != nil {
			return err
		}
		pushed = append(pushed, rel)
		if !strings.HasPrefix(rel, app+string(filepath.Separator)) {
			outside = append(outside, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pushed) == 0 {
		t.Fatal("nothing was pushed at all, so nothing was checked for being the application's own")
	}
	if len(outside) > 0 {
		t.Errorf("the release carries files that are not the application's: %v", outside)
	}
	for _, env := range []string{"production", "staging"} {
		if _, err := os.Stat(filepath.Join(scratch, "pushed", app, env, applications.Kustomization)); err != nil {
			t.Errorf("the pushed release is missing the application's %s settings: %v", env, err)
		}
	}
	kustomization, err := os.ReadFile(filepath.Join(scratch, "pushed", app, applications.Base, applications.Kustomization))
	if err != nil {
		t.Fatalf("the pushed release has no base: %v", err)
	}
	if !strings.Contains(string(kustomization), "digest: "+digest) {
		t.Errorf("the pushed base does not pin the image just built:\n%s", kustomization)
	}
	// And the pin reaches every environment's rendering.
	for _, env := range []string{"production", "staging"} {
		out, err := exec.Command("kubectl", "kustomize", filepath.Join(scratch, "pushed", app, env)).CombinedOutput()
		if err != nil {
			t.Fatalf("the pushed %s settings do not build: %v\n%s", env, err, out)
		}
		if !strings.Contains(string(out), fixtureImage+"@"+digest) {
			t.Errorf("%s as released does not run the image just built:\n%s", env, out)
		}
	}
}

// Every order that makes a release has to be one the fabricator can follow:
// a directory that is there, with settings for at least one environment; a
// base that does not already pin images - the fabricator appends that pin,
// and a second images: key is invalid YAML nobody sees until Flux does; and a
// pattern that captures the version and nothing else.
func TestEveryReleaseOrderIsOneTheFabricatorCanFollow(t *testing.T) {
	root := repoRoot(t)
	for _, problem := range unfollowableReleases(t, root) {
		t.Error(problem)
	}
}

func unfollowableReleases(t *testing.T, root string) []string {
	t.Helper()
	apps, err := applications.Read(root)
	if err != nil {
		return []string{err.Error()}
	}
	byRoot := map[string]applications.Application{}
	for _, a := range apps {
		byRoot[a.Root] = a
	}
	var problems []string
	for _, o := range ordersOf(t, root) {
		if o.Release == nil {
			continue
		}
		a, isApplication := byRoot[o.Release.Module]
		if !isApplication {
			problems = append(problems, fmt.Sprintf("%s: the release is made of %s, which is not an application's directory", o.Name, o.Release.Module))
			continue
		}
		base, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(a.Root), applications.Base, applications.Kustomization))
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("%s: %s has no base/kustomization.yaml, and that is where the fabricator pins the image", o.Name, a.Root))
		case strings.Contains(string(base), "\nimages:") || strings.HasPrefix(string(base), "images:"):
			problems = append(problems, fmt.Sprintf("%s: %s/base/kustomization.yaml already has an images: key, and the fabricator appends one", o.Name, a.Root))
		}
		envs, err := a.Environments(root)
		if err != nil {
			return []string{err.Error()}
		}
		if len(envs) == 0 {
			problems = append(problems, fmt.Sprintf("%s: %s has settings for no environment, so there is nothing a site could run from a release of it", o.Name, a.Root))
		}
		if len(o.Release.Version.Env) == 0 {
			problems = append(problems, fmt.Sprintf("%s: the release names no environment variables, so the probe starts an image that may refuse to run", o.Name))
		}
		if strings.Count(o.Release.Version.Pattern, `\(`) != 1 {
			problems = append(problems, fmt.Sprintf("%s: the version pattern must capture exactly one group, the version: %q", o.Name, o.Release.Version.Pattern))
		}
	}
	return problems
}

// The check on release orders is held to what it claims, against orders
// written here: the fixture's is followable, and each way of breaking it is
// named.
func TestUnfollowableReleasesNamesEachWayAnOrderCanBeBroken(t *testing.T) {
	if problems := unfollowableReleases(t, fabricatorFixture(t)); len(problems) != 0 {
		t.Fatalf("the fixture's release was refused: %v", problems)
	}
	app := applications.Dir + "/thing"
	for name, c := range map[string]struct {
		path, body, want string
	}{
		"a base that pins images":   {app + "/base/kustomization.yaml", "resources: [deployment.yaml]\nimages:\n  - name: x\n", "already has an images: key"},
		"no environment variables":  {app + "/" + applications.Declaration, strings.Replace(fixtureDeclaration, `["THING_NAME", "THING_KEY"]`, `[]`, 1), "names no environment variables"},
		"a pattern of two captures": {app + "/" + applications.Declaration, strings.Replace(fixtureDeclaration, `v\\(`, `\\(v\\)\\(`, 1), "exactly one group"},
	} {
		root := fabricatorFixture(t)
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(c.path)), []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if problems := unfollowableReleases(t, root); len(problems) != 1 || !strings.Contains(problems[0], c.want) {
			t.Errorf("%s: want one problem saying %q, got %v", name, c.want, problems)
		}
	}
	for name, remove := range map[string][]string{
		"no base":        {app + "/base"},
		"no environment": {app + "/production", app + "/staging"},
	} {
		root := fabricatorFixture(t)
		for _, dir := range remove {
			if err := os.RemoveAll(filepath.Join(root, filepath.FromSlash(dir))); err != nil {
				t.Fatal(err)
			}
		}
		if problems := unfollowableReleases(t, root); len(problems) != 1 || !strings.Contains(problems[0], name) {
			t.Errorf("%s: want one problem naming it, got %v", name, problems)
		}
	}
}

// --- reusing what already exists (#514) --------------------------------------

// A registry fake for the reuse steps: the owner lookup, and a package listing
// whose answer the test chooses. gh evaluates its own --jq, so the fake answers
// with what that filter would have selected.
func fakeRegistryListing(t *testing.T, listing string) string {
	t.Helper()
	return fakeTools(t, map[string]string{"gh": `if [ "$2" = "--paginate" ]; then set -- "$1" "$3"; fi
case "$2" in
  users/*) echo Organization ;;
  *) ` + listing + ` ;;
esac`})
}

// The image fingerprint is the build context's tracked files and the pins
// passed to it: the same inputs give the same fingerprint, and a change to
// either gives a new one. A build of that fingerprint already in the registry
// is reused rather than repeated.
func TestAnImageOfTheSameInputsIsReused(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.email=a@example.com", "-c", "user.name=t"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.MkdirAll(filepath.Join(repo, "img"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(repo, "img", "Dockerfile"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", "-A")
		run("commit", "-qm", "img")
	}
	fingerprint := func(pins, listing string) (map[string]string, string) {
		ok, output, logs := runReleaseStep(t, "Reuse a build of the same inputs", repo, fakeRegistryListing(t, listing),
			[]string{"OWNER=example", "PACKAGE=homelab-thing", "CONTEXT=img", "PINS=" + pins, "GH_TOKEN=x"})
		if !ok {
			t.Fatalf("the reuse step failed:\n%s", logs)
		}
		out := map[string]string{}
		for _, l := range strings.Split(strings.TrimSpace(output), "\n") {
			if k, v, ok := strings.Cut(l, "="); ok {
				out[k] = v
			}
		}
		return out, logs
	}

	write("ARG THING_BUILD_VERSION\nFROM scratch\n")
	first, _ := fingerprint("--build-arg THING_BUILD_VERSION=1", `echo "gh: Not Found (HTTP 404)" >&2; exit 1`)
	again, _ := fingerprint("--build-arg THING_BUILD_VERSION=1", `echo "gh: Not Found (HTTP 404)" >&2; exit 1`)
	if first["fingerprint"] == "" || first["fingerprint"] != again["fingerprint"] {
		t.Fatalf("the same inputs fingerprinted differently: %v then %v", first, again)
	}
	if first["digest"] != "" {
		t.Errorf("an image was reused when the registry holds no build at all: %v", first)
	}
	newPin, _ := fingerprint("--build-arg THING_BUILD_VERSION=2", `echo "gh: Not Found (HTTP 404)" >&2; exit 1`)
	if newPin["fingerprint"] == first["fingerprint"] {
		t.Error("a new pin kept the old fingerprint, so its image would never be built")
	}
	write("ARG THING_BUILD_VERSION\nFROM scratch\nUSER 1\n")
	newFile, _ := fingerprint("--build-arg THING_BUILD_VERSION=1", `echo "gh: Not Found (HTTP 404)" >&2; exit 1`)
	if newFile["fingerprint"] == first["fingerprint"] {
		t.Error("a change to the build context kept the old fingerprint, so its image would never be built")
	}

	digest := "sha256:" + strings.Repeat("c", 64)
	reused, _ := fingerprint("--build-arg THING_BUILD_VERSION=1", "echo "+digest)
	if reused["digest"] != digest {
		t.Errorf("a build of these inputs exists and was not reused: %v", reused)
	}

	ok, _, logs := runReleaseStep(t, "Reuse a build of the same inputs", repo,
		fakeRegistryListing(t, `echo "gh: Server Error (HTTP 500)" >&2; exit 1`),
		[]string{"OWNER=example", "PACKAGE=homelab-thing", "CONTEXT=img", "PINS=x", "GH_TOKEN=x"})
	if ok {
		t.Errorf("a registry that could not be read was taken to hold no build:\n%s", logs)
	}
}

// releaseFingerprint runs the workflow's own fingerprint step for the
// fixture application in root.
func releaseFingerprint(t *testing.T, root, digest, listing string) (ok bool, out map[string]string, logs string) {
	t.Helper()
	ok, output, logs := runReleaseStep(t, "Reuse a release of the same manifests", root, fakeRegistryListing(t, listing),
		[]string{"OWNER=example", "PACKAGE=homelab-thing-release", "IMAGE=" + fixtureImage,
			"DIGEST=" + digest, "RELEASE=" + fixtureRelease(t, root), "GH_TOKEN=x"})
	out = map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(output), "\n") {
		if k, v, found := strings.Cut(l, "="); found {
			out[k] = v
		}
	}
	return ok, out, logs
}

// A release is fingerprinted from the manifests it would ship, rendered for
// every environment the application has settings for, with the image pinned.
// A comment renders to nothing, so it makes no new release - the #514 case,
// where a comment was delivered as a release identical to the one running. A
// new image digest is a new release, and so is a change to any one
// environment's settings.
func TestAReleaseOfTheSameManifestsIsNotRepeated(t *testing.T) {
	heavy(t, "renders the fixture with kustomize several times over")
	root := fabricatorFixture(t)
	none := `echo "gh: Not Found (HTTP 404)" >&2; exit 1`
	a := "sha256:" + strings.Repeat("a", 64)
	fingerprint := func(digest, listing string) map[string]string {
		ok, out, logs := releaseFingerprint(t, root, digest, listing)
		if !ok {
			t.Fatalf("the release fingerprint step failed:\n%s", logs)
		}
		return out
	}
	edit := func(rel string, change func(string) string) {
		path := filepath.Join(root, filepath.FromSlash(applications.Dir+"/thing/"+rel))
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(change(string(body))), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	first := fingerprint(a, none)
	if first["needed"] != "true" {
		t.Fatalf("a release nobody has published was not needed: %v", first)
	}

	edit("production/"+applications.Kustomization, func(s string) string { return "# a comment reaches no cluster\n" + s })
	if commented := fingerprint(a, none); commented["fingerprint"] != first["fingerprint"] {
		t.Error("a comment changed the release fingerprint, so it would make a release of nothing (#514)")
	}
	if other := fingerprint("sha256:"+strings.Repeat("b", 64), none); other["fingerprint"] == first["fingerprint"] {
		t.Error("a new image kept the release fingerprint, so the new build would never be released")
	}
	if again := fingerprint(a, "printf '%s\\n' 1.0.16-1 "+first["fingerprint"]); again["needed"] != "false" {
		t.Errorf("a release of these exact manifests exists and another was needed: %v", again)
	}
	edit("staging/settings.yaml", func(s string) string { return strings.Replace(s, "level: staging", "level: changed", 1) })
	if changed := fingerprint(a, none); changed["fingerprint"] == first["fingerprint"] {
		t.Error("a change to one environment's settings kept the release fingerprint, so a site running that environment would never be given it")
	}
}

// What an application is made of is its own directory, by construction: the
// release is rendered from a copy holding that directory and nothing else, so
// a manifest that reaches for a file outside it does not build and is never
// released. And an application with settings for no environment is refused
// rather than released as nothing.
func TestAReleaseThatReachesOutsideItsApplicationIsNotMade(t *testing.T) {
	heavy(t, "renders the fixture with kustomize")
	none := `echo "gh: Not Found (HTTP 404)" >&2; exit 1`
	a := "sha256:" + strings.Repeat("a", 64)

	root := fabricatorFixture(t)
	reaching := filepath.Join(root, filepath.FromSlash(applications.Dir+"/thing/production/kustomization.yaml"))
	if err := os.WriteFile(reaching, []byte("apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - ../base\n  - settings.yaml\n  - ../../../../elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "elsewhere", applications.Kustomization), []byte("resources: [secret.yaml]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// It builds where it stands, which is why a review would not catch it.
	if out, err := exec.Command("kubectl", "kustomize", filepath.Dir(reaching)).CombinedOutput(); err != nil {
		t.Fatalf("the fixture's reach outside its directory does not build even in place, so this proves nothing: %v\n%s", err, out)
	}
	if ok, out, logs := releaseFingerprint(t, root, a, none); ok || out["fingerprint"] != "" {
		t.Errorf("a release was fingerprinted for an application whose manifests reach outside its directory: %v\n%s", out, logs)
	}

	root = fabricatorFixture(t)
	for _, env := range []string{"production", "staging"} {
		if err := os.RemoveAll(filepath.Join(root, filepath.FromSlash(applications.Dir+"/thing/"+env))); err != nil {
			t.Fatal(err)
		}
	}
	ok, _, logs := releaseFingerprint(t, root, a, none)
	if ok || !strings.Contains(logs, "settings for no environment") {
		t.Errorf("an application with settings for no environment was not refused by name:\n%s", logs)
	}
}

// --- bringing a release to the gate ------------------------------------------

// releaseDelivery is a GitHub that answers for a repository with three sites:
// one that runs the application and follows its releases, one that runs it
// and holds its pin, and one that does not run it. It records what the step
// asked it to commit.
type releaseDelivery struct{ dir, path string }

func newReleaseDelivery(t *testing.T, sites map[string]string) releaseDelivery {
	t.Helper()
	dir := t.TempDir()
	var listed []string
	for site, body := range sites {
		rel := applications.SiteFilePath(site)
		listed = append(listed, rel)
		path := filepath.Join(dir, "tree", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(listed)
	if err := os.WriteFile(filepath.Join(dir, "sites"), []byte(strings.Join(listed, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// gh evaluates its own --jq, so the fake answers with what each filter
	// would have selected.
	gh := `fake="` + dir + `"
echo "$*" >> "$fake/calls"
case "$1 $2" in
  "api users/"*) echo Organization ;;
  "api repos/example/homelab/git/ref/heads/main") echo mainsha ;;
  "api repos/example/homelab/git/commits/mainsha") echo treesha ;;
  "api repos/example/homelab/git/trees/treesha?recursive=1") [ -s "$fake/sites" ] && grep . "$fake/sites" || true ;;
  "api --paginate") echo "2026-09-30T00:00:00Z 2.4.1-3 sha256:` + strings.Repeat("b", 64) + `" ;;
  "api repos/example/homelab/contents/"*)
    path="${2#repos/example/homelab/contents/}"
    base64 -w0 "$fake/tree/${path%%\?*}" ;;
  "api repos/example/homelab/git/blobs") echo "blob-$(grep -c 'git/blobs' "$fake/calls")" ;;
  "api repos/example/homelab/git/trees") echo "$*" > "$fake/committed"; echo newtree ;;
  "api repos/example/homelab/git/commits") echo commitsha ;;
  "api repos/example/homelab/git/refs") ;;
  "pr list") ;;
  "pr create") echo "$*" > "$fake/opened"; echo https://example.invalid/pull/7 ;;
  *) echo "fake gh: unexpected $*" >&2; exit 64 ;;
esac`
	return releaseDelivery{dir, fakeTools(t, map[string]string{"gh": gh})}
}

func (f releaseDelivery) read(name string) string {
	b, err := os.ReadFile(filepath.Join(f.dir, name))
	if err != nil {
		return ""
	}
	return string(b)
}

// deliver runs the workflow's own delivery step, with the real procurement
// verb, against the fixture.
func (f releaseDelivery) deliver(t *testing.T) (ok bool, summary, logs string) {
	t.Helper()
	scratch := t.TempDir()
	cmd := exec.Command("bash", "-eo", "pipefail", "-c", fabricatorStep(t, "deliver", "Deliver"))
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "PATH="+f.path,
		"GH_TOKEN=x", "READ_TOKEN=x", "REPO=example/homelab", "OWNER=example", "REPO_NAME=homelab",
		`ORDERS=[{"name":"kit","context":"kits/kit"},{"name":"thing","context":"x","release":{"module":"x"}}]`,
		"RUN_URL=https://example.invalid/run", "RUNNER_TEMP="+scratch,
		"GITHUB_STEP_SUMMARY="+filepath.Join(scratch, "summary"), "GITHUB_OUTPUT="+filepath.Join(scratch, "output"))
	out, err := cmd.CombinedOutput()
	written, _ := os.ReadFile(filepath.Join(scratch, "summary"))
	return err == nil, string(written), string(out)
}

func deliverySource(name, tag string, digest byte, annotations string) string {
	return "---\napiVersion: source.toolkit.fluxcd.io/v1\nkind: OCIRepository\nmetadata:\n  name: " + name + "\n  namespace: flux-system" + annotations +
		"\nspec:\n  url: oci://example.invalid/homelab-" + name + "-release\n  ref:\n    tag: \"" + tag + "\"\n    digest: \"sha256:" + strings.Repeat(string(digest), 64) + "\"\n"
}

// A new release goes to every site that runs the application and follows its
// releases, in one pull request, and to no other: a site that holds its pin
// keeps what it runs, and a site that does not run the application is not
// touched. That is how two sites run two versions.
func TestADeliveryGoesToTheSitesThatFollowAndNoOther(t *testing.T) {
	heavy(t, "runs the delivery step with the real procurement verb, once per site file")
	const hold = "\n  annotations:\n    homelab.fensberg.com/delivery: hold"
	f := newReleaseDelivery(t, map[string]string{
		"north": deliverySource("thing", "2.4.1-2", 'a', ""),
		"west":  deliverySource("other", "1.0.0-1", 'c', "") + deliverySource("thing", "2.4.0-9", 'a', ""),
		"south": deliverySource("thing", "2.4.1-2", 'a', hold),
		"east":  deliverySource("other", "1.0.0-1", 'c', ""),
	})
	ok, summary, logs := f.deliver(t)
	if !ok {
		t.Fatalf("the delivery step failed:\n%s", logs)
	}
	committed := f.read("committed")
	for site, want := range map[string]bool{"north": true, "west": true, "south": false, "east": false} {
		if got := strings.Contains(committed, "tree[][path]="+applications.SiteFilePath(site)); got != want {
			t.Errorf("%s being in the delivery is %v, want %v. A release goes to the sites that follow it - a site that holds its pin, or does not run the application, is left alone.\n\ncommitted: %s", site, got, want, committed)
		}
	}
	if n := strings.Count(f.read("calls"), "git/blobs"); n != 2 {
		t.Errorf("%d files were written for the delivery, and two sites take it", n)
	}
	opened := f.read("opened")
	if !strings.Contains(opened, "--head deliver/thing-2.4.1-3") || !strings.Contains(opened, "feat(thing): release 2.4.1-3") {
		t.Errorf("the pull request is not one for this release: %s", opened)
	}
	for _, site := range []string{"north", "west"} {
		if !strings.Contains(opened, applications.SiteFilePath(site)) {
			t.Errorf("the pull request does not say it moves the pin in %s: %s", site, opened)
		}
	}
	if !strings.Contains(summary, "thing: 2.4.1-3 delivered") {
		t.Errorf("the run does not say what it delivered: %q", summary)
	}

	// Every site that follows already runs it: nothing is opened.
	f = newReleaseDelivery(t, map[string]string{
		"north": deliverySource("thing", "2.4.1-3", 'b', ""),
		"south": deliverySource("thing", "2.4.1-2", 'a', hold),
	})
	if ok, summary, logs := f.deliver(t); !ok || f.read("opened") != "" || !strings.Contains(summary, "already runs 2.4.1-3") {
		t.Errorf("with every following site on the release, a delivery was opened or the run failed (%v):\n%s\n%s", ok, summary, logs)
	}

	// No site has been given an application: there is nowhere to deliver,
	// and that is not a failure.
	f = newReleaseDelivery(t, map[string]string{})
	if ok, summary, logs := f.deliver(t); !ok || f.read("opened") != "" || !strings.Contains(summary, "nowhere to deliver") {
		t.Errorf("with no site running anything, the run failed or opened a delivery (%v):\n%s\n%s", ok, summary, logs)
	}
}
