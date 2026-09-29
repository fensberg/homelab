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
//   - Kubernetes: an object written directly in an environment, rather than
//     reached as an overlay of a base in modules/applications/.
//   - Go: a function whose signature and body are identical in two packages.
//     It catches copy-paste - four copies of one TCP dial, six such groups
//     when this was written - and NOT the same idea written differently. The
//     failure says so rather than letting a green run imply more.
//   - OpenTofu: a resource outside a module. That leg arrives with the module
//     the cluster root becomes; until then every resource is outside one.
const customBlocksFile = "tests/custom-blocks.yml"

type customBlocks struct {
	Kubernetes []struct {
		Object string `yaml:"object"`
		Reason string `yaml:"reason"`
	} `yaml:"kubernetes"`
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

func TestAKubernetesObjectOutsideABaseIsDeclared(t *testing.T) {
	root := repoRoot(t)
	declared := map[string]bool{}
	for _, d := range readCustomBlocks(t).Kubernetes {
		if strings.TrimSpace(d.Reason) == "" {
			t.Errorf("%s declares %q with no reason; the reason is the whole price of a custom block", customBlocksFile, d.Object)
		}
		declared[d.Object] = true
	}

	files := tracked(t, func(rel string) bool {
		return strings.HasPrefix(rel, "environments/") &&
			(strings.HasSuffix(rel, ".yaml") || strings.HasSuffix(rel, ".yml")) &&
			filepath.Base(rel) != "kustomization.yaml"
	})
	found := map[string]bool{}
	for _, rel := range files {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range environmentObjects(rel, body) {
			found[o] = true
		}
	}
	if len(files) == 0 {
		t.Fatal("no manifest was found under environments/, so this checked nothing")
	}
	reportCustomBlocks(t, found, declared, "Kubernetes object",
		"is written directly in an environment rather than as an overlay of a base in modules/applications/. Move it into a base and patch it here, or declare it in "+customBlocksFile+" with the reason it cannot be reused.")
}

// environmentObjects is every Kubernetes object in one manifest, as
// "<file> <Kind> <namespace>/<name>".
func environmentObjects(rel string, body []byte) []string {
	var out []string
	dec := yaml.NewDecoder(bytes.NewReader(body))
	for {
		var doc struct {
			APIVersion string `yaml:"apiVersion"`
			Kind       string `yaml:"kind"`
			Metadata   struct {
				Name      string `yaml:"name"`
				Namespace string `yaml:"namespace"`
			} `yaml:"metadata"`
		}
		if dec.Decode(&doc) != nil {
			break
		}
		// kustomize's own files describe how to build, not an object.
		if doc.Kind == "" || strings.HasPrefix(doc.APIVersion, "kustomize.config.k8s.io/") {
			continue
		}
		out = append(out, fmt.Sprintf("%s %s %s/%s", rel, doc.Kind, doc.Metadata.Namespace, doc.Metadata.Name))
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

func TestEnvironmentObjectsNamesEachObjectAndSkipsKustomize(t *testing.T) {
	got := environmentObjects("environments/p/a.yaml", []byte(`---
apiVersion: v1
kind: Service
metadata: {name: s, namespace: n}
---
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
---
apiVersion: v1
kind: ConfigMap
metadata: {name: c, namespace: n}
`))
	want := "environments/p/a.yaml Service n/s|environments/p/a.yaml ConfigMap n/c"
	if strings.Join(got, "|") != want {
		t.Fatalf("got %q", got)
	}
}
