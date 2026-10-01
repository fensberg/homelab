package repo

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"homelab/details/tofufiles"
	"homelab/tests/harness"
)

// No test reads a root's OpenTofu by where it is.
//
// WHY THIS EXISTS. A test that opens `management/<root>/talos.tf` is a test of
// that path. When the site's root became two, the tests written that way went
// on reading the root they named; one of them found nothing left to check
// there and kept passing. And every file they name is about to move again,
// into modules, where a test that names a path either breaks or - worse -
// reads a thin root that no longer holds what it is checking.
//
// So a test finds OpenTofu by what it declares (tofuDeclaring: "the file that
// declares this resource", wherever it is) or reads every file (tofuSources).
// It does not say which root. This guard reads every Go test in the
// repository and refuses the three ways of saying it:
//
//   - a path into a root: "management/<root>/anything";
//   - a path into the Flux tree: "clusters/<anything>", which is about to
//     become a shared core and a directory per site;
//   - the name of one of the repository's own OpenTofu files: "talos.tf";
//   - a root's directory built from its parts: Join(..., "management", "<root>").
//
// What it leaves alone. A root's directory named whole ("management/estate")
// is a scope, the thing every file is compared against, and that is what the
// estate-scope and kubernetes guards need. A root's tests/ directory holds
// fixtures, not what is built. And a program's own test that builds a pretend
// repository in a temporary directory is not reading this one: for tests
// under scripts/, only a file that finds the real repository is held to it.
func TestNoTestNamesTheRootItReads(t *testing.T) {
	root := repoRoot(t)
	sources := tofuSources(t)
	roots := openTofuRoots(sources)
	if len(roots) < 3 {
		t.Fatalf("found %d OpenTofu root(s), so the enumeration has stopped matching", len(roots))
	}
	names := map[string]bool{}
	for rel := range sources {
		names[filepath.Base(rel)] = true
	}

	tests := tracked(t, func(rel string) bool { return strings.HasSuffix(rel, "_test.go") })
	if len(tests) < 100 {
		t.Fatalf("only %d Go test files were read, so the enumeration has stopped matching", len(tests))
	}
	read := 0
	for _, rel := range tests {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		if !readsTheRealRepository(rel, string(body)) {
			continue
		}
		read++
		problems, err := rootsNamed(rel, body, roots, names)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range problems {
			t.Error(p)
		}
	}
	if read < 50 {
		t.Fatalf("only %d test files were held to this, so the rule for which ones read the repository has stopped matching", read)
	}
}

// readsTheRealRepository says whether a test file's paths are paths in this
// repository. Every test tier's are. A program's own test only when it finds
// the repository: the rest build what they need in a temporary directory.
func readsTheRealRepository(rel, body string) bool {
	if strings.HasPrefix(rel, "tests/") {
		return true
	}
	return strings.Contains(body, "repopath.")
}

// rootsNamed is each place a test file names a root's files rather than
// finding them.
func rootsNamed(rel string, src []byte, roots []string, tofuNames map[string]bool) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return nil, fmt.Errorf("%s does not parse, so what it names cannot be read: %w", rel, err)
	}
	found := map[string]bool{}
	say := func(n ast.Node, what string) {
		found[fmt.Sprintf("%s:%d %s Find it by what it declares (tofuDeclaring or fluxObject; tofufiles.Declaring in a program's test) or read every file (tofuSources): the next root, the module this moves into, or the site's own directory is then read without anybody editing this test.", rel, fset.Position(n.Pos()).Line, what)] = true
	}
	literal := func(e ast.Expr) (string, bool) {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return "", false
		}
		v, err := strconv.Unquote(lit.Value)
		return v, err == nil
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BasicLit:
			v, ok := literal(x)
			// Prose is not a path. A message may say where something was
			// found; it is the reading that must not.
			if !ok || strings.ContainsAny(v, " \n\t") {
				return true
			}
			for _, r := range roots {
				if rest, in := strings.CutPrefix(v, r+"/"); in && !strings.HasPrefix(rest, "tests/") && rest != "tests" {
					say(x, fmt.Sprintf("names %q, a path into the root %s.", v, r))
					return true
				}
			}
			// A pattern enumerates; it is the reading of one named place that
			// is refused.
			if rest, in := strings.CutPrefix(v, fluxTree+"/"); in && rest != "" && !strings.ContainsAny(rest, "*?[") {
				say(x, fmt.Sprintf("names %q, a path into the Flux tree.", v))
				return true
			}
			if base := v[strings.LastIndex(v, "/")+1:]; strings.HasSuffix(base, ".tf") && tofuNames[base] {
				say(x, fmt.Sprintf("names the OpenTofu file %q.", v))
			}
		case *ast.CallExpr:
			for i := 0; i+1 < len(x.Args); i++ {
				a, okA := literal(x.Args[i])
				b, okB := literal(x.Args[i+1])
				if !okA || !okB {
					continue
				}
				// The Flux tree, then a directory in it: the same path, in
				// parts.
				if a == fluxTree && !strings.ContainsAny(b, "*?[") {
					say(x, fmt.Sprintf("builds a path into the Flux tree from its parts (%s, %s).", a, b))
					continue
				}
				for _, r := range roots {
					if a+"/"+b != r {
						continue
					}
					if i+2 < len(x.Args) {
						if next, ok := literal(x.Args[i+2]); ok && next == "tests" {
							continue
						}
					}
					say(x, fmt.Sprintf("builds the root %s from its parts.", r))
				}
			}
		}
		return true
	})
	out := make([]string, 0, len(found))
	for p := range found {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// What the guards find OpenTofu by: the declaration each is about, in the
// words the code uses. Named once, so a rename is one edit here and every
// guard that reads the thing follows it.
const (
	declMachineConfig = `data "talos_machine_configuration" "controlplane"`
	declClusterHealth = `data "talos_cluster_health"`
	declMachines      = `resource "proxmox_virtual_environment_vm" "talos_cp"`
	declPools         = `resource "proxmox_virtual_environment_pool" "control_plane"`
	declInvariants    = `resource "terraform_data" "invariants"`
)

// tofuAll is the code of every OpenTofu file, one after another in path
// order, for a check that asks whether something is declared anywhere.
func tofuAll(t *testing.T) string {
	t.Helper()
	sources := tofuSources(t)
	paths := make([]string, 0, len(sources))
	for p := range sources {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var b strings.Builder
	for _, p := range paths {
		b.WriteString(tofufiles.Code(sources[p]))
		b.WriteString("\n")
	}
	return b.String()
}

// The Flux tree: what each cluster reconciles. Named whole it is a scope; a
// path into it names where a manifest is today.
const fluxTree = harness.FluxTree

// What the guards find a manifest by: an object it declares.
const (
	kindHelmRelease = "HelmRelease"
	// The Flux controllers' own install, vendored as `flux bootstrap` wrote it.
	fluxInstallKind, fluxInstallName = "Deployment", "source-controller"
	// The CNI, rendered from its chart for Talos to apply.
	cniKind, cniName = "DaemonSet", "cilium"
)

// fluxObjectPath is the one tracked manifest in the Flux tree that declares
// an object of this kind and name, wherever in the tree it is. The reader is
// the harness's, so every tier finds a manifest the same way.
func fluxObjectPath(kind, name string) (string, error) {
	return harness.FluxManifest(kind, name)
}

// fluxObject is that manifest's path and body. The test fails if no manifest
// declares the object or several do.
func fluxObject(t *testing.T, kind, name string) (path, body string) {
	t.Helper()
	path, err := fluxObjectPath(kind, name)
	if err != nil {
		t.Fatal(err)
	}
	return path, readRepoFile(t, path)
}

// beside is a file in the same directory as a manifest found by what it
// declares.
func beside(manifest, name string) string { return harness.Beside(manifest, name) }

var (
	tofuOnce  sync.Once
	tofuFiles map[string]string
	tofuErr   error
)

// tofuSources is every OpenTofu file the repository authored, by path: its
// roots and its modules, and none of their tests' fixtures.
func tofuSources(t *testing.T) map[string]string {
	t.Helper()
	tofuOnce.Do(func() { tofuFiles, tofuErr = tofufiles.Read(repoRoot(t)) })
	if tofuErr != nil {
		t.Fatal(tofuErr)
	}
	return tofuFiles
}

// tofuDeclaring is the one OpenTofu file that declares something, wherever it
// is, and its body. The test fails if none does or several do.
func tofuDeclaring(t *testing.T, declaration string) (path, body string) {
	t.Helper()
	path, body, err := tofufiles.Declaring(tofuSources(t), declaration)
	if err != nil {
		t.Fatal(err)
	}
	return path, body
}

func TestRootsNamedRefusesEachWayOfNamingARoot(t *testing.T) {
	roots := []string{"ground/alpha", "ground/beta"}
	names := map[string]bool{"widgets.tf": true}
	file := func(body string) []byte {
		return []byte("package x\n\nfunc f() {\n" + body + "\n}\n")
	}
	for name, tc := range map[string]struct {
		body string
		want string
	}{
		"a path into a root":             {`read("ground/alpha/widgets.tf")`, "a path into the root ground/alpha"},
		"a directory inside a root":      {`walk("ground/beta/parts")`, "a path into the root ground/beta"},
		"a file's name alone":            {`readFrom(dir, "widgets.tf")`, `names the OpenTofu file "widgets.tf"`},
		"a file's name on a built path":  {`read(prefix + "/widgets.tf")`, `names the OpenTofu file "/widgets.tf"`},
		"a root built from its parts":    {`join(top, "ground", "alpha")`, "builds the root ground/alpha from its parts"},
		"the root as a scope":            {`const scope = "ground/alpha"`, ""},
		"a root's fixtures":              {`read("ground/alpha/tests/fixtures/valid.json")`, ""},
		"a root's fixtures, from parts":  {`join(top, "ground", "alpha", "tests", "fixtures")`, ""},
		"prose that mentions a path":     {`fail("ground/alpha/widgets.tf no longer says so")`, ""},
		"a file nobody has by that name": {`write(dir, "scratch.tf")`, ""},
		"another directory":              {`read("elsewhere/alpha/thing.json")`, ""},
		"a path into the Flux tree":      {`read("` + fluxTree + `/somewhere/thing.yaml")`, "a path into the Flux tree"},
		"the Flux tree as a scope":       {`under(rel, "` + fluxTree + `/")`, ""},
		"a pattern over the Flux tree":   {`glob("` + fluxTree + `/*/thing.yaml")`, ""},
		"a Flux path built from parts":   {`join(top, "` + fluxTree + `", "somewhere", "thing.yaml")`, "builds a path into the Flux tree from its parts"},
	} {
		body := tc.body
		if strings.HasPrefix(body, "const") {
			body = "_ = 0\n}\n" + body + "\nfunc g() {"
		}
		got, err := rootsNamed("x_test.go", file(body), roots, names)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		joined := strings.Join(got, "|")
		if (tc.want == "") != (joined == "") || !strings.Contains(joined, tc.want) {
			t.Errorf("%s: got %q, want %q", name, joined, tc.want)
		}
	}
	if _, err := rootsNamed("x_test.go", []byte("package x\nfunc broken( {"), roots, names); err == nil {
		t.Error("a test file that does not parse was called clean")
	}
	if !readsTheRealRepository("tests/go/tier/a_test.go", "") || !readsTheRealRepository("scripts/p/a_test.go", "root, _ := repopath.Root()") || readsTheRealRepository("scripts/p/a_test.go", "dir := t.TempDir()") {
		t.Error("which tests read the real repository is decided wrongly")
	}
}
