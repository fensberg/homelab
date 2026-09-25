package repo

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"sort"
	"strings"
	"testing"

	"homelab/contractor/config"
)

// The estate creates a site's buckets and the contractor uses them, and the two
// must agree about which buckets there are.
//
// management/estate/site/object-storage.tf creates one bucket per purpose and
// grants a key for each; buckets.go is how the contractor knows which granted
// bucket is which, and which one a teardown empties. Neither can be derived
// from the other - OpenTofu cannot read a Go table - so they are written twice
// and held together here.
//
// A purpose the estate creates and the table lacks is a bucket the site is
// granted and never uses, and never empties. A purpose the table has and the
// estate lacks is a credential the site's config asks the vault for and never
// receives, so every run fails at Render naming a field rather than a bucket.
func TestTheBucketTableAgreesWithTheHCL(t *testing.T) {
	hcl := bucketPurposesInHCL(t)
	var table []string
	for _, b := range config.Buckets {
		table = append(table, b.Key)
	}
	sort.Strings(table)
	if strings.Join(hcl, " ") != strings.Join(table, " ") {
		t.Errorf("the estate creates buckets for %v and buckets.go knows %v.\n\n"+
			"A purpose on one side only is a bucket granted and never used, or a grant the "+
			"site asks for and never receives. Change both.", hcl, table)
	}
}

// The teardown empties a bucket it resolves from the table, not the raw config
// value.
//
// Those two are the same string only while the database bucket carries an
// empty suffix. Writing the shorter one works today, survives review, and
// starts emptying the wrong bucket the day somebody renames it believing the
// rename to be cosmetic.
func TestTheTeardownResolvesTheBucketItEmptiesFromTheTable(t *testing.T) {
	body := readRepoFile(t, "scripts/contractor/internal/phases/teardown.go")
	if !strings.Contains(body, `config.BucketByKey("database")`) {
		t.Error(`teardown.go does not resolve the bucket it empties with config.BucketByKey("database").` + "\n\n" +
			"Emptying is the irreversible half of a demolish. It must name the bucket that is " +
			"declared destroyable, rather than whichever bucket name happened to be in the " +
			"config - those agree today and are one rename apart from disagreeing.")
	}
}

var purposesRe = regexp.MustCompile(`(?s)purposes\s*=\s*toset\(\[(.*?)\]\)`)

func bucketPurposesInHCL(t *testing.T) []string {
	t.Helper()
	body := readRepoFile(t, "management/estate/site/object-storage.tf")
	m := purposesRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("management/estate/site/object-storage.tf has no `purposes = toset([...])`, so this " +
			"guard reads nothing. If the set was renamed, rename it here too.")
	}
	var out []string
	for _, q := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(m[1], -1) {
		out = append(out, q[1])
	}
	if len(out) == 0 {
		t.Fatal("the estate declares an empty set of bucket purposes, so this guard compares nothing")
	}
	sort.Strings(out)
	return out
}

func stripLineComments(body string) string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// Backup and Restore reach the state backups through the one resolver.
//
// They are the two ends of one pipe, and the way this breaks is silent in the
// worst direction: a restore pointed somewhere else finds nothing and reports
// that there is no backup, at the moment somebody is recovering an estate.
//
// This replaces a guard that read both files for config.BucketByKey("state").
// The resolution then moved into config.StateBackupLocation, and for a while
// nothing checked either end still used it - a regression of the review that
// introduced it. Read from the syntax tree: a mention in a comment is not a call.
func TestBackupAndRestoreReachTheStateBackupsThroughOneResolver(t *testing.T) {
	for _, file := range []string{
		"scripts/contractor/internal/phases/backup.go",
		"scripts/contractor/internal/phases/restore.go",
	} {
		if !callsSelector(t, file)["config.StateBackupLocation"] {
			t.Errorf("%s does not call config.StateBackupLocation.\n\n"+
				"Backup writes the age-encrypted state dumps and Restore reads them back. If "+
				"either resolves the location some other way the two can drift apart, and the "+
				"symptom is a restore that finds nothing during a recovery.", file)
		}
	}
}

// Nothing resolves the state backups by hand.
//
// The guard above names two files, and a list of two files is the thing that
// goes stale: a third reader - a new tier, a new verb, a script - would not be
// on it. This one walks every Go file and refuses a hand-built route to the
// state backups outside the config package, so every reader, present and
// future, goes through StateBackupLocation.
func TestNothingResolvesTheStateBackupsByHand(t *testing.T) {
	const home = "scripts/contractor/config/"
	byHand := []string{"config.StateBackupPath", "config.LatestStateBackupPath"}

	for _, rel := range goFiles(t) {
		if strings.HasPrefix(rel, home) {
			continue
		}
		calls := callsSelector(t, rel)
		for _, c := range byHand {
			if calls[c] {
				t.Errorf("%s calls %s, building the route to the state backups itself.\n\n"+
					"Use config.StateBackupLocation, which Backup and Restore both use - "+
					"a third route is a third place for the location to drift.", rel, c)
			}
		}
		if calls[`config.BucketByKey("state")`] {
			t.Errorf("%s resolves the state bucket with config.BucketByKey(\"state\").\n\n"+
				"Use config.StateBackupLocation: the bucket alone is half the answer, and the "+
				"other half - which credential reaches it - is where copies disagreed before.", rel)
		}
	}
}

// callsSelector is every pkg.Func call in a Go file, plus pkg.Func("arg") for a
// call whose first argument is a string literal, so a guard can tell a lookup
// of one key from a lookup of another.
func callsSelector(t *testing.T, rel string) map[string]bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), rel, readRepoFile(t, rel), parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", rel, err)
	}
	out := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		name := pkg.Name + "." + sel.Sel.Name
		out[name] = true
		if len(call.Args) > 0 {
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				out[name+"("+lit.Value+")"] = true
			}
		}
		return true
	})
	return out
}
