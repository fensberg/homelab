package repo

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"homelab/contractor/steps"
	"homelab/tests/harness"
	"homelab/tests/parsed"
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
// So a test finds OpenTofu by what it declares (the tree's Declaring: "the
// block that declares this resource", wherever it is) or reads every file
// (the tree's OpenTofu).
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
	tree := repository(t)
	tofu := tree.OpenTofu(t)
	roots := parsed.OpenTofuRoots(tofu)
	if len(roots) < 3 {
		t.Fatalf("found %d OpenTofu root(s), so the enumeration has stopped matching", len(roots))
	}
	names := map[string]bool{}
	for _, f := range tofu {
		names[path.Base(f.Path)] = true
	}

	tests := tracked(t, func(rel string) bool { return strings.HasSuffix(rel, "_test.go") })
	if len(tests) < 100 {
		t.Fatalf("only %d Go test files were read, so the enumeration has stopped matching", len(tests))
	}
	read := 0
	for _, rel := range tests {
		fset := token.NewFileSet()
		f := tree.Go(t, fset, rel)
		if !readsTheRealRepository(rel, f) {
			continue
		}
		read++
		for _, p := range rootsNamed(rel, fset, f, roots, names) {
			t.Error(p)
		}
	}
	if read < 50 {
		t.Fatalf("only %d test files were held to this, so the rule for which ones read the repository has stopped matching", read)
	}
}

// readsTheRealRepository says whether a test file's paths are paths in this
// repository. Every test tier's are. A program's own test only when it finds
// the repository, which it does by importing what finds it: the rest build
// what they need in a temporary directory.
func readsTheRealRepository(rel string, f *ast.File) bool {
	if strings.HasPrefix(rel, "tests/") {
		return true
	}
	return slices.ContainsFunc(f.Imports, func(spec *ast.ImportSpec) bool { return spec.Path.Value == strconv.Quote("homelab/details/repopath") })
}

// rootsNamed is each place a test file names a root's files rather than
// finding them.
func rootsNamed(rel string, fset *token.FileSet, f *ast.File, roots []string, tofuNames map[string]bool) []string {
	found := map[string]bool{}
	say := func(n ast.Node, what string) {
		found[fmt.Sprintf("%s:%d %s Find it by what it declares (the tree's Declaring or fluxObject; tofufiles.Declaring in a program's test) or read every file (the tree's OpenTofu): the next root, the module this moves into, or the site's own directory is then read without anybody editing this test.", rel, fset.Position(n.Pos()).Line, what)] = true
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
	return out
}

// What the guards find OpenTofu by: the declaration each is about, as its
// kind and labels. Named once, so a rename is one edit here and every guard
// that reads the thing follows it.
var (
	declMachineConfig = []string{"data", "talos_machine_configuration", "controlplane"}
	declClusterHealth = []string{"data", "talos_cluster_health"}
	declMachines      = []string{"resource", steps.MachineResource, "talos_cp"}
	declPools         = []string{"resource", "proxmox_virtual_environment_pool", "control_plane"}
	declInvariants    = []string{"resource", "terraform_data", "invariants"}
)

// declaring is the one block a declaration names, through the repository's
// reader.
func declaring(t *testing.T, declaration []string) parsed.Block {
	t.Helper()
	return repository(t).Declaring(t, declaration[0], declaration[1:]...)
}

// tofuFile is the OpenTofu file at a path, parsed, as the tree reads it.
func tofuFile(t *testing.T, rel string) parsed.OpenTofuFile {
	t.Helper()
	for _, f := range repository(t).OpenTofu(t) {
		if f.Path == rel {
			return f
		}
	}
	t.Fatalf("%s is not an OpenTofu file the repository authored", rel)
	return parsed.OpenTofuFile{}
}

// objectItems is every key and value written in an object anywhere in a
// node: a yamlencode({...}) patch, a local's map, a for expression's result.
func objectItems(n hclsyntax.Node) []hclsyntax.ObjectConsItem {
	var out []hclsyntax.ObjectConsItem
	_ = hclsyntax.VisitAll(n, func(n hclsyntax.Node) hcl.Diagnostics {
		if obj, ok := n.(*hclsyntax.ObjectConsExpr); ok {
			out = append(out, obj.Items...)
		}
		return nil
	})
	return out
}

// refersTo says whether anything in a node refers to a value by that name or
// to a part of it: refersTo(block, "local.net") holds for local.net.pod_cidr.
func refersTo(n hclsyntax.Node, name string) bool {
	found := false
	_ = hclsyntax.VisitAll(n, func(n hclsyntax.Node) hcl.Diagnostics {
		if ref, ok := n.(*hclsyntax.ScopeTraversalExpr); ok {
			if got := traversalName(ref.Traversal); got == name || strings.HasPrefix(got, name+".") {
				found = true
			}
		}
		return nil
	})
	return found
}

// traversalName is a reference as it is written, up to its first index:
// local.net.pod_cidr, var.site.
func traversalName(tr hcl.Traversal) string {
	var parts []string
	for _, step := range tr {
		switch s := step.(type) {
		case hcl.TraverseRoot:
			parts = append(parts, s.Name)
		case hcl.TraverseAttr:
			parts = append(parts, s.Name)
		default:
			return strings.Join(parts, ".")
		}
	}
	return strings.Join(parts, ".")
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
func fluxObjectPath(t *testing.T, kind, name string) (string, error) {
	t.Helper()
	return repository(t).FluxObjectPath(t, kind, name)
}

// fluxObject is that manifest's path and every document it holds, decoded.
// The test fails if no manifest declares the object or several do.
func fluxObject(t *testing.T, kind, name string) (path string, docs []any) {
	t.Helper()
	return repository(t).FluxObject(t, kind, name)
}

// beside is a file in the same directory as a manifest found by what it
// declares.
func beside(manifest, name string) string { return harness.Beside(manifest, name) }

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
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "x_test.go", file(body), 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := rootsNamed("x_test.go", fset, f, roots, names)
		joined := strings.Join(got, "|")
		if (tc.want == "") != (joined == "") || !strings.Contains(joined, tc.want) {
			t.Errorf("%s: got %q, want %q", name, joined, tc.want)
		}
	}
	program := func(src string) *ast.File {
		f, err := parser.ParseFile(token.NewFileSet(), "a_test.go", src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	finds := program("package p\nimport \"homelab/details/repopath\"\n")
	makesItsOwn := program("package p\nimport \"os\"\n// repopath.Root() is not called here\n")
	if !readsTheRealRepository("tests/go/tier/a_test.go", makesItsOwn) || !readsTheRealRepository("scripts/p/a_test.go", finds) || readsTheRealRepository("scripts/p/a_test.go", makesItsOwn) {
		t.Error("which tests read the real repository is decided wrongly")
	}
}
