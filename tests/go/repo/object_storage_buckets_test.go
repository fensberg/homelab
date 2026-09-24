package repo

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"testing"
)

// The bucket set is declared twice, and the two declarations must agree.
//
// object-storage.tf creates the buckets; buckets.go decides what happens to
// each one at teardown. Neither side can be derived from the other - OpenTofu
// cannot read a Go table, and the teardown is a Go program that runs when no
// OpenTofu is loaded - so they are written twice on purpose, the same
// defence-in-depth the config contract already uses.
//
// The failure this catches is specific and silent. Add a bucket to the HCL and
// not to the table and it is created, filled, and then destroyed by the next
// teardown with a zero exit code, because nothing told Sterilize to release
// it. Add it to the table and not to the HCL and the teardown tries to release
// a resource that was never created, which is noisy and harmless - so the
// dangerous direction is the one a reviewer is least likely to notice.
func TestTheBucketTableAgreesWithTheHCL(t *testing.T) {
	hcl := bucketsDeclaredInHCL(t)
	table := bucketsDeclaredInGo(t)

	for key, hclSuffix := range hcl {
		tableSuffix, ok := table[key]
		if !ok {
			t.Errorf("object-storage.tf declares the bucket %q and buckets.go does not.\n\n"+
				"It will be created and then destroyed by the next teardown, silently, because "+
				"nothing tells Sterilize to release it. Add it to config.Buckets with the "+
				"Keep value that says whether it outlives the estate.", key)
			continue
		}
		if hclSuffix != tableSuffix {
			t.Errorf("the two declarations disagree about the name of the %q bucket: "+
				"object-storage.tf appends %q, buckets.go appends %q.\n\n"+
				"They resolve to two different real buckets, so whichever side the teardown "+
				"and the adopt read will be pointed at a bucket the other one never created.",
				key, hclSuffix, tableSuffix)
		}
	}

	for key := range table {
		if _, ok := hcl[key]; !ok {
			t.Errorf("buckets.go declares the bucket %q and object-storage.tf does not.\n\n"+
				"Nothing creates it, so the teardown will try to release a resource that is "+
				"not in state and the adopt will look for a bucket that does not exist.", key)
		}
	}
}

// Every bucket that survives a teardown is released before anything empties or
// destroys anything.
//
// This replaces the single-bucket version of the same guard. The order is the
// safety: releasing first means a failure to release stops short of deleting
// anything, while the other way round the deletion has already happened by the
// time anyone finds out.
//
// A cluster rebuild is the routine way a new Talos version reaches these
// machines (#97), and a rebuild is a demolish followed by an ignition - so the
// teardown runs across the things people would most mind losing every time the
// OS moves.
func TestBucketsThatOutliveTheEstateAreReleasedBeforeAnythingDeletes(t *testing.T) {
	body := readRepoFile(t, "scripts/contractor/internal/phases/sterilize.go")

	forget := strings.Index(body, "forgetKeptBuckets(ctx)")
	empty := strings.Index(body, "emptyObjectStorage(ctx)")

	if forget < 0 {
		t.Fatal("Sterilize never calls forgetKeptBuckets.\n\n" +
			"Without it the destroy tries to delete the buckets holding the state dumps and " +
			"every workload's data. Cloudflare refuses to delete a bucket with objects in " +
			"it, so the teardown stops with the machines still running - and emptying them " +
			"to get past that is how the data is lost.")
	}
	if empty < 0 {
		t.Fatal("Sterilize never calls emptyObjectStorage, so this test proves nothing.\n\n" +
			"Either the teardown stopped emptying the database bucket or the call was renamed.")
	}
	if forget > empty {
		t.Errorf("Sterilize empties object storage before it releases the kept buckets.\n\n"+
			"Order is the safety here. Releasing first means a failure to release stops short "+
			"of deleting anything; the other way round, the deletion has already happened by "+
			"the time anyone finds out.\n\n"+
			"  forgetKeptBuckets at %d, emptyObjectStorage at %d", forget, empty)
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

// Reads each `resource "cloudflare_r2_bucket" "<key>"` block and the `name =`
// line inside it, so the guard compares real bucket names rather than only
// keys. Split-and-scan rather than one regex: a single pattern spanning a
// block that contains both braces and interpolation is the kind of regex that
// silently matches nothing, and a guard matching nothing passes.
// Reads each `resource "cloudflare_r2_bucket" "<key>"` block and the `name =`
// line inside it, so the guard compares real bucket names rather than only
// keys. Split-and-scan rather than one regex: a single pattern spanning a
// block that contains both braces and interpolation is the kind of regex that
// silently matches nothing, and a guard matching nothing passes.
var (
	hclNameRe   = regexp.MustCompile(`(?m)^\s*name\s*=\s*(.+?)\s*$`)
	hclSuffixRe = regexp.MustCompile(`^"\$\{local\.site_name\}([^"]*)"$`)
	goBucketRe  = regexp.MustCompile(`Key:\s*"([^"]+)",\s*Suffix:\s*"([^"]*)"`)
)

const bucketResourcePrefix = `resource "cloudflare_r2_bucket" "`

// The one bucket still named from the config rather than the site slug. It
// cannot move in place - Cloudflare refuses to delete a bucket holding objects,
// and this one holds the WAL archive - so the rename waits for a rebuild. When
// that happens this entry goes, and the guard starts requiring every bucket to
// be named for the site.
const configuredNameBucket = "database"

func bucketsDeclaredInHCL(t *testing.T) map[string]string {
	t.Helper()
	body := readRepoFile(t, "management/cluster/object-storage.tf")

	found := map[string]string{}
	for _, chunk := range strings.Split(body, bucketResourcePrefix)[1:] {
		quote := strings.Index(chunk, `"`)
		if quote < 0 {
			t.Fatal("a cloudflare_r2_bucket resource has no closing quote on its name")
		}
		key := chunk[:quote]

		m := hclNameRe.FindStringSubmatch(chunk)
		if m == nil {
			t.Errorf("bucket resource %q has no `name =` line", key)
			continue
		}
		name := m[1]

		if _, dup := found[key]; dup {
			t.Errorf("the bucket resource %q is declared twice in object-storage.tf", key)
		}

		switch {
		case key == configuredNameBucket && name == "local.object_storage.bucket":
			found[key] = ""
		case hclSuffixRe.MatchString(name):
			found[key] = hclSuffixRe.FindStringSubmatch(name)[1]
		case name == "local.object_storage.bucket":
			t.Errorf("bucket %q is named from local.object_storage.bucket, and only %q may be.\n\n"+
				"Buckets are named for the site, because the site is the isolation boundary and "+
				"every site in an estate shares one object storage account. Use "+
				"\"${local.site_name}-<suffix>\".", key, configuredNameBucket)
		default:
			t.Errorf("bucket %q is named %s, which is derived from neither local.site_name "+
				"nor local.object_storage.bucket.\n\n"+
				"Bucket names come from the vault so that a fork changes one field and no "+
				"real name appears in this file. A literal here is a name in git.", key, name)
		}
	}

	if len(found) == 0 {
		t.Fatal("no cloudflare_r2_bucket resources found in management/cluster/object-storage.tf.\n\n" +
			"Either they were renamed or their shape changed, in which case this guard is " +
			"reading nothing and proving nothing.")
	}
	return found
}

func bucketsDeclaredInGo(t *testing.T) map[string]string {
	t.Helper()
	body := readRepoFile(t, "scripts/contractor/config/buckets.go")

	start := strings.Index(body, "var Buckets = []Bucket{")
	if start < 0 {
		t.Fatal("buckets.go has no `var Buckets` slice. " +
			"If it was renamed, rename it here too rather than deleting this guard.")
	}

	found := map[string]string{}
	for _, m := range goBucketRe.FindAllStringSubmatch(stripLineComments(body[start:]), -1) {
		if _, dup := found[m[1]]; dup {
			t.Errorf("the bucket key %q is declared twice in buckets.go", m[1])
		}
		found[m[1]] = m[2]
	}
	if len(found) == 0 {
		t.Fatal("no buckets found in scripts/contractor/config/buckets.go.\n\n" +
			"Either the Buckets slice was renamed or its shape changed, in which case this " +
			"guard is reading nothing and proving nothing.")
	}
	return found
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
