package repo

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Every function the tests do not run is declared, or the build fails.
//
// The coverage baseline is one number over a growing program, so it catches a
// collapse and misses drift: adding forty uncovered lines to scripts/contractor
// moves the total by a fraction of a percent. That is not a hypothetical shape
// of failure here - the baseline sat at 24.5 while the real figure was 34.1,
// ten points of slack, for long enough that nobody remembered setting it.
//
// This is the per-function version, kept readable by counting per file. A new
// untested function raises its file's count and fails until somebody writes
// the new number down; a newly tested one lowers it and fails the same way,
// so the file tightens as the program gets better tested rather than sagging.
// A file with no entry may have no untested functions at all.
//
// WHAT COUNTS AS RUN. A function is tested when more than half of its
// statements are run, and a function is anything with a body: a declaration,
// or a function written as a value at the top of a file.
//
// It used to be "any statement at all", read from `go tool cover -func`, and
// both halves of that were holes one change walked through. A check was added
// to the top of a function nothing ran, a test ran the check, and the
// function left this ledger with its original body - a write and the
// read-back that verifies it - exactly as untested as before. And the call
// that had made it untestable was moved into a function held in a variable,
// which that report does not list, so the one piece of code the change was
// for had no test and nothing here to say so.
//
// More than half is a line, and an arbitrary one. It is drawn where a
// function's own work, and not only a guard at its door, has to have run.
//
// It deliberately does not require coverage. Most of this program shells out
// to tofu, ansible, talosctl, kubectl, op and rclone against a real estate,
// and no amount of rule-making changes that. What it requires is that the
// decision has been made and written down, so an untested decision cannot sit
// unnoticed among plumbing that genuinely cannot be tested.

type coverageExemptions struct {
	Files []struct {
		Path      string `yaml:"path"`
		Uncovered int    `yaml:"uncovered"`
		CoveredBy string `yaml:"covered_by"`
	} `yaml:"files"`
}

// The tiers an entry may name. "nothing" is deliberately spellable: a gap
// somebody has looked at and written down is worth more than one nobody has,
// and refusing to allow the honest answer would only produce dishonest ones.
var knownTiers = map[string]bool{
	"e2e": true, "integration": true, "api": true,
	"not-worth-testing": true, "nothing": true,
}

// uncoveredByFile runs the shipped program's own tests with coverage and
// returns how many functions in each file are untested.
//
// Measured rather than read from a checked-in profile: a profile committed
// alongside the code it describes is a second copy that drifts, and the whole
// point here is to catch drift.
func uncoveredByFile(t *testing.T) map[string][]string {
	t.Helper()
	root := repoRoot(t)
	counts := map[string][]string{}

	// Every module, not just the contractor.
	//
	// This looked at scripts/contractor alone, so a function with no coverage
	// anywhere else was invisible. The operator's question is the one that
	// matters here: "I want to know if in two weeks we've built a function that
	// itself has no coverage." For six of the seven modules the answer was no -
	// including security, whose functions refuse unsigned pushes and
	// unapproved deliveries, and signedpush, which reads the App private key.
	//
	// Paths are prefixed with the module, so two modules with an internal/run
	// cannot collide and the file says which program each entry is about.
	for _, module := range goModules(t) {
		dir := filepath.Join(root, "scripts", module)

		profile := filepath.Join(t.TempDir(), module+".out")
		build := exec.Command("go", "test", "-covermode=atomic", "-coverprofile="+profile, "./...")
		build.Dir = dir
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("running the %s tests with coverage: %v\n%s", module, err, out)
		}
		raw, err := os.ReadFile(profile)
		if err != nil {
			t.Fatalf("reading the %s coverage profile: %v", module, err)
		}
		blocks, err := coverageBlocks(string(raw))
		if err != nil {
			t.Fatalf("the %s coverage profile: %v", module, err)
		}

		prefix := "homelab/" + module + "/"
		for file, in := range blocks {
			rel, ok := strings.CutPrefix(file, prefix)
			if !ok {
				t.Fatalf("the %s coverage profile names %s, which is not a file of that module", module, file)
			}
			src, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
			if err != nil {
				t.Fatalf("reading %s to find its functions: %v", rel, err)
			}
			untested, err := untestedFunctions(rel, src, in)
			if err != nil {
				t.Fatalf("%s/%s: %v", module, rel, err)
			}
			if len(untested) > 0 {
				counts[module+"/"+rel] = untested
			}
		}
	}
	if len(counts) == 0 {
		t.Fatal("the coverage profiles reported no untested functions at all, which " +
			"is not these programs - the reading of the profile has stopped matching its shape")
	}
	return counts
}

// coverageBlock is one line of a coverage profile: a run of statements, where
// it starts, and whether a test ran it.
type coverageBlock struct {
	line, col  int
	statements int
	ran        bool
}

// coverageBlocks reads a profile into its blocks, by file.
func coverageBlocks(profile string) (map[string][]coverageBlock, error) {
	// homelab/details/onepassword/upsert.go:109.44,112.16 3 0
	shape := regexp.MustCompile(`^(.+):(\d+)\.(\d+),\d+\.\d+ (\d+) (\d+)$`)
	out := map[string][]coverageBlock{}
	for _, line := range strings.Split(strings.TrimSpace(profile), "\n") {
		if strings.HasPrefix(line, "mode:") || line == "" {
			continue
		}
		m := shape.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("a line is not a coverage block: %q", line)
		}
		b := coverageBlock{ran: m[5] != "0"}
		b.line, _ = strconv.Atoi(m[2])
		b.col, _ = strconv.Atoi(m[3])
		b.statements, _ = strconv.Atoi(m[4])
		out[m[1]] = append(out[m[1]], b)
	}
	return out, nil
}

// untestedFunctions is the functions of one file that tests run half of or
// less, by name. A function is a declaration with a body, or a function
// written as a value outside any declaration - which is the form a variable
// takes when a test is meant to replace it, and so the form most likely to
// hold what no test runs.
func untestedFunctions(name string, src []byte, blocks []coverageBlock) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("does not parse: %w", err)
	}

	type function struct {
		name       string
		from, to   token.Position
		total, ran int
	}
	var functions []*function
	add := func(name string, body *ast.BlockStmt) {
		if body != nil {
			functions = append(functions, &function{name: name, from: fset.Position(body.Pos()), to: fset.Position(body.End())})
		}
	}
	for _, d := range file.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			add(d.Name.Name, d.Body)
		case *ast.GenDecl:
			ast.Inspect(d, func(n ast.Node) bool {
				if lit, ok := n.(*ast.FuncLit); ok {
					add("a function at line "+strconv.Itoa(fset.Position(lit.Pos()).Line), lit.Body)
					return false
				}
				return true
			})
		}
	}

	before := func(aLine, aCol, bLine, bCol int) bool { return aLine < bLine || aLine == bLine && aCol <= bCol }
	for _, b := range blocks {
		for _, f := range functions {
			if before(f.from.Line, f.from.Column, b.line, b.col) && before(b.line, b.col, f.to.Line, f.to.Column) {
				f.total += b.statements
				if b.ran {
					f.ran += b.statements
				}
				break
			}
		}
	}

	var untested []string
	for _, f := range functions {
		if f.total > 0 && f.ran*2 <= f.total {
			untested = append(untested, f.name)
		}
	}
	return untested, nil
}

func TestEveryUncoveredFunctionIsDeclared(t *testing.T) {
	heavy(t, "runs every module's tests with coverage")
	root := repoRoot(t)

	body, err := os.ReadFile(filepath.Join(root, "tests", "coverage-exemptions.yml"))
	if err != nil {
		t.Fatalf("reading tests/coverage-exemptions.yml: %v", err)
	}
	var declared coverageExemptions
	if err := yaml.Unmarshal(body, &declared); err != nil {
		t.Fatalf("parsing tests/coverage-exemptions.yml: %v", err)
	}
	if len(declared.Files) == 0 {
		t.Fatal("the exemptions file declares nothing, so this test would pass over " +
			"a program with no coverage at all")
	}

	want := map[string]int{}
	for _, f := range declared.Files {
		if _, dup := want[f.Path]; dup {
			t.Errorf("%s is declared twice; the second entry silently wins", f.Path)
		}
		want[f.Path] = f.Uncovered

		if !knownTiers[f.CoveredBy] {
			t.Errorf(`%s declares covered_by: %q, which is not a tier.

Use e2e, integration or api to name the tier that reaches it;
not-worth-testing for printing and process control; or nothing, which is
allowed and is the point - a gap somebody wrote down beats one nobody did.`,
				f.Path, f.CoveredBy)
		}
	}

	actual := uncoveredByFile(t)

	var paths []string
	for p := range want {
		paths = append(paths, p)
	}
	for p := range actual {
		if _, ok := want[p]; !ok {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)

	for _, p := range paths {
		got, declaredCount := len(actual[p]), want[p]
		which := strings.Join(actual[p], ", ")
		switch {
		case got == declaredCount:
			continue

		case declaredCount == 0:
			t.Errorf(`%s has %d untested function(s) and no entry in tests/coverage-exemptions.yml: %s.

Either test them, or add an entry saying why they cannot be - naming what the
tested decision is, if one was extracted. An undeclared uncovered function is
the case this file exists to make impossible: something nobody decided about,
sitting among plumbing that was decided about.`, p, got, which)

		case got > declaredCount:
			t.Errorf(`%s has %d untested function(s); tests/coverage-exemptions.yml says %d. They are: %s.

Something was added without a test, or a test stopped running most of one. Add one, or raise the number here and say
in the commit message why that function cannot be covered.`, p, got, declaredCount, which)

		default:
			t.Errorf(`%s has %d untested function(s); tests/coverage-exemptions.yml says %d.

Coverage improved - lower the number to %d so the file keeps ratcheting. Left
alone it becomes slack, which is exactly how the coverage baseline came to sit
ten points below reality.`, p, got, declaredCount, got)
		}
	}
}

// The two ways a function used to leave the ledger without being tested,
// each as the smallest file that shows it.
const guardedAtTheDoor = `package p

func write(item string) error {
	if item == "" {
		return errEmpty
	}
	raw := fetch(item)
	changed := apply(raw)
	store(changed)
	return verify(item)
}

var run = func(args ...string) error {
	out := start(args)
	return wait(out)
}

func covered() int {
	a := one()
	b := two()
	return a + b
}
`

func blocksOf(t *testing.T, profile string) []coverageBlock {
	t.Helper()
	blocks, err := coverageBlocks(profile)
	if err != nil {
		t.Fatal(err)
	}
	return blocks["p/p.go"]
}

// A test that runs the check at the top of a function and nothing below it
// has not tested the function.
func TestAFunctionWhoseDoorAloneIsRunIsUntested(t *testing.T) {
	blocks := blocksOf(t, `mode: atomic
p/p.go:3.31,4.16 1 4
p/p.go:4.16,6.3 1 4
p/p.go:7.2,10.21 4 0
p/p.go:13.38,16.2 2 3
p/p.go:18.20,22.2 3 1
`)
	got, err := untestedFunctions("p.go", []byte(guardedAtTheDoor), blocks)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "write" {
		t.Fatalf("untested: %v, want write alone. Two of its six statements ran, and they are the check at its door", got)
	}
}

// A function held in a variable is a function, and one no test runs is
// counted. `go tool cover -func` does not list it at all.
func TestAFunctionHeldInAVariableIsCounted(t *testing.T) {
	blocks := blocksOf(t, `mode: atomic
p/p.go:3.31,4.16 1 4
p/p.go:4.16,6.3 1 4
p/p.go:7.2,10.21 4 4
p/p.go:13.38,16.2 2 0
p/p.go:18.20,22.2 3 1
`)
	got, err := untestedFunctions("p.go", []byte(guardedAtTheDoor), blocks)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0], "line 13") {
		t.Fatalf("untested: %v, want the function at line 13 and nothing else", got)
	}
}

func TestAProfileLineThatIsNotABlockIsRefused(t *testing.T) {
	if _, err := coverageBlocks("mode: atomic\np/p.go: something else\n"); err == nil {
		t.Fatal("a profile this cannot read was read as a file with nothing untested")
	}
}
