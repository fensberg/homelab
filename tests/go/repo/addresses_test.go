package repo

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The contractor writes a resource address in one place:
// scripts/contractor/steps (#497).
//
// Eight addresses were string literals spread through the phases, so the
// converge's sequence was hand-written in two files and the plan's was
// something else again. Now the steps are declared once and every other
// address is a constant beside them - which is also what makes turning the
// cluster root into a module one edit, when every address gains a prefix.
// A literal written anywhere else is the drift coming back.
//
// String literals only, read by the Go parser: a comment naming a resource is
// documentation.
var resourceAddress = regexp.MustCompile(`^(data\.)?(proxmox|talos|kubernetes|tailscale|terraform|random|helm|cloudflare|tls)_[a-z0-9_]+\.[a-z0-9_]+`)

func TestTheContractorWritesAResourceAddressInOnePlace(t *testing.T) {
	root := repoRoot(t)
	files := tracked(t, func(rel string) bool {
		return strings.HasPrefix(rel, "scripts/contractor/") && strings.HasSuffix(rel, ".go") &&
			!strings.HasSuffix(rel, "_test.go") && rel != "scripts/contractor/steps/steps.go"
	})
	if len(files) < 10 {
		t.Fatalf("only %d contractor files were read, so this checked almost nothing", len(files))
	}
	for _, rel := range files {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		for _, where := range addressLiterals(t, rel, body) {
			t.Errorf(`%s writes a resource address.

Name it in scripts/contractor/steps: as a step, if the converge applies it, or
as a constant beside them. That file is the one place an address is written, so
the plan and the converge cannot disagree about one and a move into a module is
one edit.`, where)
		}
	}
}

func addressLiterals(t *testing.T, rel string, body []byte) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, body, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", rel, err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if v, err := strconv.Unquote(lit.Value); err == nil && resourceAddress.MatchString(v) {
			out = append(out, fset.Position(lit.Pos()).String())
		}
		return true
	})
	return out
}

func TestAddressLiteralsAreFoundAndProseIsNot(t *testing.T) {
	src := "package p\n// talos_cluster_kubeconfig.this, in a comment\nvar a = \"talos_cluster_kubeconfig.this\"\nvar b = \"data.talos_cluster_health.this[0]\"\nvar c = \"talos_version\"\nvar d = `tofu apply (compute: vms)`\n"
	if got := addressLiterals(t, "p.go", []byte(src)); len(got) != 2 {
		t.Errorf("found %v, want the two literals", got)
	}
}
