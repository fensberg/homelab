package parsed

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Proof is one entry of a mutation ledger: the guard it proves, what it
// breaks and how, and what the guard's failure must say.
//
// Only this package makes one, by reading a ledger, and only a Proof made so
// may be asked where its text is in a file or applied to a copy. A guard that
// wanted a file's text could otherwise wrap any string in a Proof and ask.
type Proof struct {
	Guard string `yaml:"guard"`
	// Rule is, for a proof of a rule harness, the rule that must be among
	// those refusing.
	Rule string `yaml:"rule"`
	File string `yaml:"file"`
	// Test is, in an application's own ledger, which of its tests must be the
	// one that failed.
	Test     string `yaml:"test"`
	Find     string `yaml:"find"`
	Replace  string `yaml:"replace"`
	Create   string `yaml:"create"`
	Mentions string `yaml:"mentions"`
	Why      string `yaml:"why"`
	// ReadsSource opts a Go target in, for a guard that reads the source as
	// data rather than running it.
	ReadsSource bool `yaml:"reads_source"`
	// Source is where the proof is written: its file in the estate's ledger,
	// or its place in an application's.
	Source string `yaml:"-"`

	loaded bool
}

// Creates reports whether the proof plants a file rather than editing one.
func (p Proof) Creates() bool { return strings.TrimSpace(p.Create) != "" }

// Proof is the one proof a ledger file holds.
func (r *Tree) Proof(t testing.TB, rel string) Proof {
	t.Helper()
	var p Proof
	r.YAMLInto(t, rel, &p)
	p.Source, p.loaded = baseName(rel), true
	return p
}

// ProofsIn is the proofs a ledger file lists under mutations:, each known by
// the file and its place in it.
func (r *Tree) ProofsIn(t testing.TB, rel string) []Proof {
	t.Helper()
	var l struct {
		Mutations []Proof `yaml:"mutations"`
	}
	if err := yaml.Unmarshal(r.read(t, rel), &l); err != nil {
		t.Fatalf("parsing %s: %v", rel, err)
	}
	for i := range l.Mutations {
		l.Mutations[i].Source, l.Mutations[i].loaded = fmt.Sprintf("%s#%d", rel, i), true
	}
	return l.Mutations
}

// LogicProofs is a logic ledger: proofs that a program's tests assert on a
// decision rather than merely run it. Each names the decision as its guard,
// the test that must fail as its test, and a file under base, the program's
// directory. The ledger's floor comes with them.
func (r *Tree) LogicProofs(t testing.TB, rel, base string) (floor int, proofs []Proof) {
	t.Helper()
	var l struct {
		Floor int `yaml:"floor"`
		Logic []struct {
			Decision string `yaml:"decision"`
			File     string `yaml:"file"`
			Find     string `yaml:"find"`
			Replace  string `yaml:"replace"`
			MustFail string `yaml:"must_fail"`
			Why      string `yaml:"why"`
		} `yaml:"logic"`
	}
	r.YAMLInto(t, rel, &l)
	for i, e := range l.Logic {
		proofs = append(proofs, Proof{
			Guard: e.Decision, Test: e.MustFail, Why: e.Why,
			File: base + "/" + e.File, Find: e.Find, Replace: e.Replace,
			Source: fmt.Sprintf("%s#%d", rel, i), loaded: true,
		})
	}
	return l.Floor, proofs
}

func baseName(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[i+1:]
	}
	return rel
}

func mustBeLoaded(t testing.TB, p Proof) {
	t.Helper()
	if !p.loaded {
		t.Fatalf("a proof of %s was not read from a ledger; only a ledger's proofs are applied or placed", p.Guard)
	}
}

// Places is how many times a proof's find is in the file it names, or -1
// when the file is not there.
func (r *Tree) Places(t testing.TB, p Proof) int {
	t.Helper()
	mustBeLoaded(t, p)
	if !r.Exists(p.File) {
		return -1
	}
	body := r.read(t, p.File)
	return strings.Count(string(body), p.Find)
}

// Apply breaks a scratch tree the way a proof says, and reports whether the
// tree changed. The tree is indexed after, so a planted file is tracked.
func (r *Tree) Apply(t testing.TB, p Proof) bool {
	t.Helper()
	mustBeLoaded(t, p)
	r.mustBeScratch(t, "break")
	if p.Creates() {
		r.Write(t, p.File, p.Create)
		r.Index(t)
		return true
	}
	changed := r.Replace(t, p.File, p.Find, p.Replace)
	r.Index(t)
	return changed
}

// Inner reports whether this run is one a meta test started against a
// scratch tree: the guards run, the harness does not.
func Inner() bool { return os.Getenv(RootEnv) != "" }

// StripYAMLComments takes every whole-line comment out of a scratch tree's
// workflows, its scripts' YAML, the pre-commit configuration and the
// taskfile - the configuration the guards read - so a meta test can ask
// which guards a comment was satisfying, and says how many it took.
func (r *Tree) StripYAMLComments(t testing.TB) int {
	t.Helper()
	r.mustBeScratch(t, "strip")
	removed := 0
	for _, rel := range r.Files(t) {
		yml := strings.HasSuffix(rel, ".yml") || strings.HasSuffix(rel, ".yaml")
		read := strings.HasPrefix(rel, ".github/workflows/") || strings.HasPrefix(rel, "scripts/") ||
			rel == ".pre-commit-config.yaml" || rel == "taskfile.yml"
		if !yml || !read {
			continue
		}
		var kept []string
		for _, line := range strings.Split(string(r.read(t, rel)), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				removed++
				continue
			}
			kept = append(kept, line)
		}
		r.Write(t, rel, strings.Join(kept, "\n"))
	}
	return removed
}
