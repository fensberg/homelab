package repo

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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
//   - Kubernetes: needs no registry. An environment is a list of
//     applications, each an overlay of the module of the same name, and the
//     only objects it may add are ones that configure or reach what the
//     module runs - never the thing that runs. That is a shape, so it is
//     checked as one and names no application: removing one is removing its
//     directories, with nothing here to edit.
//   - Go: a function whose signature and body are identical in two packages.
//     It catches copy-paste - four copies of one TCP dial, six such groups
//     when this was written - and NOT the same idea written differently. The
//     failure says so rather than letting a green run imply more.
//   - OpenTofu: a resource outside a module. That leg arrives with the module
//     the cluster root becomes; until then every resource is outside one.
const customBlocksFile = "tests/custom-blocks.yml"

type customBlocks struct {
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

func TestAnEnvironmentHoldsOnlyOverlaysOfModules(t *testing.T) {
	root := repoRoot(t)
	files := tracked(t, func(rel string) bool { return strings.HasPrefix(rel, "environments/") })
	lists := 0
	for _, rel := range files {
		for _, p := range environmentProblems(rel, func(name string) ([]byte, error) {
			return os.ReadFile(filepath.Join(root, name))
		}) {
			t.Error(p)
		}
		if isApplicationList(rel) {
			lists++
		}
	}
	if lists == 0 {
		t.Fatal("no environments/<env>/applications/kustomization.yaml was found, so this checked nothing")
	}
}

// environmentProblems is what is wrong with one tracked file under
// environments/. An environment is a list of applications and, per
// application, an overlay: the module's base plus what differs here. What
// differs - its settings, how it is reached - belongs to the environment and
// needs no declaration; the thing that runs comes from the module. So nothing
// here names an application, and removing one is removing its directories.
func environmentProblems(rel string, read func(string) ([]byte, error)) []string {
	parts := strings.Split(rel, "/")
	switch {
	case rel == "environments/README.md":
		return nil
	case isApplicationList(rel):
		k, err := readKustomization(read, rel)
		if err != nil {
			return []string{err.Error()}
		}
		var out []string
		for _, r := range k.Resources {
			if strings.Contains(r, "/") || filepath.Ext(r) != "" {
				out = append(out, fmt.Sprintf("%s lists %q. An environment lists application directories beside it, each an overlay of a module; a manifest here would be an application with no module.", rel, r))
			}
		}
		return out
	case len(parts) == 5 && parts[2] == "applications":
		app := parts[3]
		if parts[4] == "kustomization.yaml" {
			k, err := readKustomization(read, rel)
			if err != nil {
				return []string{err.Error()}
			}
			base := "../../../../modules/applications/" + app + "/base"
			for _, r := range k.Resources {
				if r == base {
					return nil
				}
			}
			return []string{fmt.Sprintf("%s does not build on %s. An environment's application is an overlay of the module of the same name, so what runs is written once and every environment reuses it.", rel, base)}
		}
		body, err := read(rel)
		if err != nil {
			return []string{err.Error()}
		}
		var out []string
		for _, o := range environmentObjects(body) {
			if workloadKinds[o.kind] {
				out = append(out, fmt.Sprintf("%s declares %s %s. What runs comes from the module's base; the environment says only how it is configured and reached. Move it into modules/applications/%s/base and patch it here.", rel, o.kind, o.name, app))
			}
		}
		return out
	}
	return []string{fmt.Sprintf("%s is not part of an environment's application overlay. environments/<env>/applications/<app>/ is the only shape here.", rel)}
}

func isApplicationList(rel string) bool {
	parts := strings.Split(rel, "/")
	return len(parts) == 4 && parts[2] == "applications" && parts[3] == "kustomization.yaml"
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

func TestEnvironmentProblemsRefusesWhatIsNotAnOverlay(t *testing.T) {
	files := map[string]string{
		"environments/p/applications/kustomization.yaml":       "resources: [app, stray.yaml]\n",
		"environments/p/applications/app/kustomization.yaml":   "resources: [../../../../modules/applications/app/base, settings.yaml]\n",
		"environments/p/applications/app/settings.yaml":        "kind: ConfigMap\nmetadata: {name: c}\n---\nkind: Service\nmetadata: {name: s}\n",
		"environments/p/applications/app/runs.yaml":            "kind: ConfigMap\nmetadata: {name: c}\n---\nkind: Deployment\nmetadata: {name: d, namespace: n}\n",
		"environments/p/applications/other/kustomization.yaml": "resources: [../../../../modules/applications/app/base]\n",
		"environments/p/loose.yaml":                            "kind: ConfigMap\n",
		"environments/README.md":                               "",
	}
	read := func(rel string) ([]byte, error) { return []byte(files[rel]), nil }
	want := map[string]string{
		"environments/p/applications/kustomization.yaml":       `lists "stray.yaml"`,
		"environments/p/applications/app/kustomization.yaml":   "",
		"environments/p/applications/app/settings.yaml":        "",
		"environments/p/applications/app/runs.yaml":            "declares Deployment n/d",
		"environments/p/applications/other/kustomization.yaml": "does not build on ../../../../modules/applications/other/base",
		"environments/p/loose.yaml":                            "is not part of",
		"environments/README.md":                               "",
	}
	for rel, w := range want {
		got := strings.Join(environmentProblems(rel, read), "|")
		if (w == "") != (got == "") || !strings.Contains(got, w) {
			t.Errorf("%s: got %q, want %q", rel, got, w)
		}
	}
}
