package repo

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"homelab/details/tofufiles"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"homelab/details/applications"
)

// A custom block is refused unless somebody says why.
//
// The rule (docs/epochs/02-abstraction.md): reuse the reusable block that
// exists, or build a reusable one, and only build something custom as a
// decision made out loud. This cannot judge reusability. It refuses what is
// undeclared, so an omission and a considered exception never look the same,
// and it refuses a declaration that no longer matches anything, so the list
// cannot fill with exceptions for things that are gone. Declarations live in
// tests/custom-blocks.yml.
//
// Per layer, because the check differs:
//
//   - Kubernetes: needs no registry. An application is one directory: a base,
//     which is the thing that runs, and beside it one directory of settings
//     for each environment, which builds on that base and may add only
//     objects that configure or reach what the base runs - never the thing
//     that runs. Neither reaches outside the application's directory. That
//     is a shape, so it is checked as one and names no application: removing
//     one is removing its directory, with nothing here to edit.
//   - Go: a function whose signature and body are identical in two packages.
//     It catches copy-paste - four copies of one TCP dial, six such groups
//     when this was written - and NOT the same idea written differently. The
//     failure says so rather than letting a green run imply more.
//   - OpenTofu: a resource declared in a root. A root is providers,
//     credentials and state; what it builds is a module's, so a second site
//     is the same module called again. Roots are found by what a root is, so
//     the next one is held to this without being listed.
const customBlocksFile = "tests/custom-blocks.yml"

type customBlocks struct {
	OpenTofu []struct {
		Resource string `yaml:"resource"`
		Reason   string `yaml:"reason"`
	} `yaml:"opentofu"`
	Go []struct {
		Functions []string `yaml:"functions"`
		Reason    string   `yaml:"reason"`
	} `yaml:"go"`
}

func readCustomBlocks(t *testing.T) customBlocks {
	t.Helper()
	var c customBlocks
	if err := yaml.Unmarshal([]byte(readRepoFile(t, customBlocksFile)), &c); err != nil {
		t.Fatalf("%s: %v", customBlocksFile, err)
	}
	return c
}

func TestAnApplicationsSettingsBuildOnItsBaseAndStayInItsDirectory(t *testing.T) {
	root := repoRoot(t)
	// No floor on how many there are: an estate with no application has
	// none, and what this refuses is proved against applications written
	// here, in TestApplicationShapeProblemsRefusesWhatIsNotTheShape.
	for _, rel := range tracked(t, func(rel string) bool { return strings.HasPrefix(rel, applications.Dir+"/") }) {
		for _, p := range applicationShapeProblems(rel, func(name string) ([]byte, error) {
			return os.ReadFile(filepath.Join(root, name))
		}) {
			t.Error(p)
		}
	}
}

// What an application keeps beside its base and its environments' settings,
// none of which Flux reconciles.
var notSettings = map[string]bool{applications.Base: true, "image": true, applicationTestsDir: true}

// applicationShapeProblems is what is wrong with one tracked file under the
// applications directory. An application is a base - the thing that runs -
// and, per environment, its settings: the base plus what differs there. What
// differs - its settings, how it is reached - is the environment's and needs
// no declaration; the thing that runs is the base's. And nothing an
// application builds from is outside its own directory, so removing one is
// removing that directory.
func applicationShapeProblems(rel string, read func(string) ([]byte, error)) []string {
	parts := strings.Split(strings.TrimPrefix(rel, applications.Dir+"/"), "/")
	// A file of the application's own beside its directories: its
	// declaration, its pins, its notes. And anything in a directory Flux
	// does not reconcile.
	if len(parts) < 3 || (notSettings[parts[1]] && parts[1] != applications.Base) {
		return nil
	}
	app, dir := parts[0], parts[1]
	if len(parts) == 3 && parts[2] == applications.Kustomization {
		k, err := readKustomization(read, rel)
		if err != nil {
			return []string{err.Error()}
		}
		var out []string
		onBase := false
		for _, r := range k.Resources {
			switch {
			case dir != applications.Base && r == "../"+applications.Base:
				onBase = true
			case strings.Contains(r, ".."), strings.HasPrefix(r, "/"), strings.Contains(r, "://"):
				out = append(out, fmt.Sprintf("%s builds on %q. An application is made of its own directory and nothing else: %s/%s/. A release holds only that directory, so anything outside it is not there to build on.", rel, r, applications.Dir, app))
			}
		}
		if dir != applications.Base && !onBase {
			out = append(out, fmt.Sprintf("%s does not build on ../%s. An environment's settings are the application's base plus what differs there, so what runs is written once and every environment reuses it.", rel, applications.Base))
		}
		return out
	}
	if dir == applications.Base || !(strings.HasSuffix(rel, ".yaml") || strings.HasSuffix(rel, ".yml")) {
		return nil
	}
	body, err := read(rel)
	if err != nil {
		return []string{err.Error()}
	}
	var out []string
	for _, o := range environmentObjects(body) {
		if workloadKinds[o.kind] {
			out = append(out, fmt.Sprintf("%s declares %s %s. What runs comes from the application's base; an environment says only how it is configured and reached. Move it into %s/%s/%s and patch it here.", rel, o.kind, o.name, applications.Dir, app, applications.Base))
		}
	}
	return out
}

var workloadKinds = map[string]bool{
	"Deployment": true, "StatefulSet": true, "DaemonSet": true,
	"Job": true, "CronJob": true, "Pod": true, "ReplicaSet": true,
}

type kustomizationResources struct {
	Resources []string `yaml:"resources"`
}

func readKustomization(read func(string) ([]byte, error), rel string) (kustomizationResources, error) {
	var k kustomizationResources
	body, err := read(rel)
	if err != nil {
		return k, err
	}
	if err := yaml.Unmarshal(body, &k); err != nil {
		return k, fmt.Errorf("%s: %w", rel, err)
	}
	return k, nil
}

type environmentObject struct{ kind, name string }

// environmentObjects is every Kubernetes object in one manifest.
func environmentObjects(body []byte) []environmentObject {
	var out []environmentObject
	dec := yaml.NewDecoder(bytes.NewReader(body))
	for {
		var doc struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name      string `yaml:"name"`
				Namespace string `yaml:"namespace"`
			} `yaml:"metadata"`
		}
		if dec.Decode(&doc) != nil {
			break
		}
		if doc.Kind != "" {
			out = append(out, environmentObject{doc.Kind, doc.Metadata.Namespace + "/" + doc.Metadata.Name})
		}
	}
	return out
}

func TestAResourceOutsideAModuleIsDeclared(t *testing.T) {
	declared := map[string]bool{}
	for _, d := range readCustomBlocks(t).OpenTofu {
		if strings.TrimSpace(d.Reason) == "" {
			t.Errorf("%s declares %q with no reason; the reason is the whole price of a custom block", customBlocksFile, d.Resource)
		}
		declared[d.Resource] = true
	}
	sources := tofuSources(t)
	roots := openTofuRoots(sources)
	if len(roots) < 3 {
		t.Fatalf("found %d OpenTofu root(s), so the enumeration has stopped matching", len(roots))
	}
	found := map[string]bool{}
	for _, r := range resourcesInRoots(sources, roots) {
		found[r] = true
	}
	reportCustomBlocks(t, found, declared, "OpenTofu resource",
		"is declared in a root rather than in a module. A root holds providers, credentials and state; what it builds goes in a module under modules/infrastructure/, so the next site calls the same block. Move it, or declare it in "+customBlocksFile+" with the reason it can have only one caller.")
}

var rootResource = regexp.MustCompile(`(?m)^resource\s+"([a-z0-9_]+)"\s+"([A-Za-z0-9_]+)"`)

// resourcesInRoots is every resource declared in a file directly in a root,
// as "<type>.<name>".
func resourcesInRoots(sources map[string]string, roots []string) []string {
	var out []string
	for rel, body := range sources {
		if !slices.Contains(roots, filepath.ToSlash(filepath.Dir(rel))) {
			continue
		}
		for _, m := range rootResource.FindAllStringSubmatch(tofufiles.Code(body), -1) {
			out = append(out, m[1]+"."+m[2])
		}
	}
	sort.Strings(out)
	return out
}

func TestAGoFunctionWrittenTwiceIsDeclared(t *testing.T) {
	root := repoRoot(t)
	declared := map[string]bool{}
	for _, d := range readCustomBlocks(t).Go {
		if strings.TrimSpace(d.Reason) == "" {
			t.Errorf("%s declares %v with no reason", customBlocksFile, d.Functions)
		}
		fns := append([]string(nil), d.Functions...)
		sort.Strings(fns)
		declared[strings.Join(fns, "  ")] = true
	}

	files := tracked(t, func(rel string) bool { return strings.HasSuffix(rel, ".go") })
	if len(files) < 100 {
		t.Fatalf("only %d Go files were read, so the enumeration has stopped matching", len(files))
	}
	sources := map[string][]byte{}
	for _, rel := range files {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		sources[rel] = body
	}
	groups, err := duplicateFunctions(sources)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, g := range groups {
		found[g] = true
	}
	reportCustomBlocks(t, found, declared, "Go function group",
		"has the same signature and body in more than one package. Keep one - homelab/details is where anything two programs share belongs - or declare the group in "+customBlocksFile+" with the reason. (This finds copies only; the same idea written differently is the declaration's to catch.)")
}

// duplicateFunctions groups functions of three or more statements whose
// signature and body print identically, and returns each group that spans
// more than one package directory as its sorted members joined by two spaces.
// Names are not compared: a copy renamed is still a copy.
func duplicateFunctions(sources map[string][]byte) ([]string, error) {
	byShape := map[string][]string{}
	for rel, src := range sources {
		f, err := parser.ParseFile(token.NewFileSet(), rel, src, 0)
		if err != nil {
			// A file that does not parse cannot be compared, and going on
			// would report "no copies" for it.
			return nil, fmt.Errorf("could not compare %s, so a copy there would go unseen: %w", rel, err)
		}
		addShapes(byShape, rel, f)
	}
	var groups []string
	for _, members := range byShape {
		dirs := map[string]bool{}
		for _, m := range members {
			dirs[m[:strings.LastIndex(m, ".")]] = true
		}
		if len(dirs) < 2 {
			continue
		}
		sort.Strings(members)
		groups = append(groups, strings.Join(members, "  "))
	}
	sort.Strings(groups)
	return groups, nil
}

// addShapes records each function of three or more statements in f by the
// printed form of its signature and body.
func addShapes(byShape map[string][]string, rel string, f *ast.File) {
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil || len(fd.Body.List) < 3 {
			continue
		}
		var b bytes.Buffer
		_ = printer.Fprint(&b, token.NewFileSet(), fd.Type)
		b.WriteByte(' ')
		_ = printer.Fprint(&b, token.NewFileSet(), fd.Body)
		byShape[b.String()] = append(byShape[b.String()], filepath.Dir(rel)+"."+fd.Name.Name)
	}
}

// reportCustomBlocks fails on anything found and undeclared, and anything
// declared and no longer found.
func reportCustomBlocks(t *testing.T, found, declared map[string]bool, what, why string) {
	t.Helper()
	var undeclared, stale []string
	for f := range found {
		if !declared[f] {
			undeclared = append(undeclared, f)
		}
	}
	for d := range declared {
		if !found[d] {
			stale = append(stale, d)
		}
	}
	sort.Strings(undeclared)
	sort.Strings(stale)
	for _, u := range undeclared {
		t.Errorf("%s %q %s", what, u, why)
	}
	for _, s := range stale {
		t.Errorf("%s declares %q, and no such %s exists any more. Remove the declaration, so the list holds only exceptions that are real.", customBlocksFile, s, what)
	}
}

func TestDuplicateFunctionsFindsCopiesAcrossPackagesOnly(t *testing.T) {
	body := func(pkg, name string) []byte {
		return []byte("package " + pkg + "\nfunc " + name + "(s string) int {\n\tn := len(s)\n\tn++\n\treturn n\n}\n")
	}
	got, err := duplicateFunctions(map[string][]byte{
		"a/x.go": body("a", "count"),
		"b/y.go": body("b", "tally"), // renamed, still a copy
		"c/z.go": []byte("package c\nfunc count(s string) int {\n\tn := len(s)\n\tn += 2\n\treturn n\n}\n"),
		"d/p.go": body("d", "one"),
		"d/q.go": body("d", "two"), // same package: not this guard's question
		"e/r.go": []byte("package e\nfunc short() int { return 1 }\n"),
		"f/s.go": []byte("package f\nfunc short() int { return 1 }\n"), // too small to matter
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a.count  b.tally  d.one  d.two"}
	if _, err := duplicateFunctions(map[string][]byte{"g/t.go": []byte("package g\nfunc broken( {\n")}); err == nil || !strings.Contains(err.Error(), "g/t.go") {
		t.Errorf("a file that does not parse was not reported: %v", err)
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestApplicationShapeProblemsRefusesWhatIsNotTheShape(t *testing.T) {
	at := func(rel string) string { return applications.Dir + "/" + rel }
	files := map[string]string{
		at("app/" + applications.Declaration):  "{}",
		at("app/README.md"):                    "",
		at("app/base/kustomization.yaml"):      "resources: [deployment.yaml]\n",
		at("app/base/deployment.yaml"):         "kind: Deployment\nmetadata: {name: d, namespace: n}\n",
		at("app/image/Dockerfile"):             "FROM scratch\n",
		at("app/tests/fixture.yaml"):           "kind: Deployment\nmetadata: {name: d}\n",
		at("app/p/kustomization.yaml"):         "resources: [../base, settings.yaml]\n",
		at("app/p/settings.yaml"):              "kind: ConfigMap\nmetadata: {name: c}\n---\nkind: Service\nmetadata: {name: s}\n",
		at("app/p/runs.yaml"):                  "kind: ConfigMap\nmetadata: {name: c}\n---\nkind: Deployment\nmetadata: {name: d, namespace: n}\n",
		at("app/p/notes.txt"):                  "kind: Deployment\n",
		at("alone/s/kustomization.yaml"):       "resources: [settings.yaml]\n",
		at("reaching/base/kustomization.yaml"): "resources: [deployment.yaml, ../../app/base]\n",
		at("reaching/s/kustomization.yaml"):    "resources: [../base, ../../../../clusters/core]\n",
		at("remote/s/kustomization.yaml"):      "resources: [../base, https://example.invalid/thing]\n",
		at("unreadable/s/kustomization.yaml"):  "resources: {\n",
	}
	read := func(rel string) ([]byte, error) { return []byte(files[rel]), nil }
	want := map[string]string{
		at("app/p/runs.yaml"):                  "declares Deployment n/d",
		at("alone/s/kustomization.yaml"):       "does not build on ../base",
		at("reaching/base/kustomization.yaml"): `builds on "../../app/base"`,
		at("reaching/s/kustomization.yaml"):    `builds on "../../../../clusters/core"`,
		at("remote/s/kustomization.yaml"):      `builds on "https://example.invalid/thing"`,
		at("unreadable/s/kustomization.yaml"):  "unreadable/s/kustomization.yaml",
	}
	for rel := range files {
		got, w := strings.Join(applicationShapeProblems(rel, read), "|"), want[rel]
		if (w == "") != (got == "") || !strings.Contains(got, w) {
			t.Errorf("%s: got %q, want %q", rel, got, w)
		}
	}
}

func TestResourcesInRootsFindsOnlyWhatARootItselfDeclares(t *testing.T) {
	sources := map[string]string{
		"ground/alpha/access.tf":      "provider \"x\" {}\nresource \"x_thing\" \"here\" {}\n# resource \"x_thing\" \"described\" {}\ndata \"x_thing\" \"read\" {}\n",
		"ground/alpha/part/inside.tf": "resource \"x_thing\" \"in_a_module_beside_it\" {}\n",
		"parts/shared/a.tf":           "resource \"x_thing\" \"in_a_module\" {}\n",
	}
	got := resourcesInRoots(sources, openTofuRoots(sources))
	if strings.Join(got, " ") != "x_thing.here" {
		t.Errorf("got %v, want only the resource the root itself declares", got)
	}
}
