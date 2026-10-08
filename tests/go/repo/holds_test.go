package repo

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"homelab/contractor/config"
	"homelab/details/applications"
	"homelab/details/holds"
)

// Everything that keeps data says what it holds, and for how long.
//
// The safety officer clears a teardown by reading what the site holds that
// is worth keeping (scripts/details/holds), so it knows only what it is
// told. An application or a controller that takes a volume and declares
// nothing is, to the officer, holding nothing: the teardown is cleared and
// the data goes with it. So every object in the repository's own manifests
// that declares storage has to be named by a `holds` entry of whoever owns
// it - the application whose directory it is in, or the core - and an entry
// that names something no longer here is refused too, because it is a
// promise about nothing.
func TestEverythingThatKeepsDataSaysWhatItHolds(t *testing.T) {
	root := repoRoot(t)
	apps, err := applications.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	core, err := holds.ReadCore(root)
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]map[string]bool{holds.Core: {}}
	for _, a := range core {
		declared[holds.Core][a.HeldBy] = true
	}
	for _, app := range apps {
		declared[app.Name] = map[string]bool{}
		copies := false
		for _, a := range app.Holds {
			declared[app.Name][a.HeldBy] = true
			copies = copies || a.Copy != nil
		}
		// An application given a key to a bucket puts something there. One
		// that names no copy has not said what.
		takesStorage := false
		for _, keys := range app.Secrets {
			for _, source := range keys {
				takesStorage = takesStorage || source.Storage != ""
			}
		}
		if takesStorage && !copies {
			t.Errorf("%s is given a key to a bucket and holds nothing with a copy, so nothing says what it puts there or how old that may be", app.Path)
		}
	}

	keeps := storageDeclared(t)
	if len(keeps) == 0 {
		t.Fatal("found nothing in the repository that declares storage, so this looked for nothing")
	}
	found := map[string]map[string]bool{}
	for _, k := range keeps {
		if found[k.owner] == nil {
			found[k.owner] = map[string]bool{}
		}
		found[k.owner][k.object] = true
		if !declared[k.owner][k.object] {
			t.Errorf(`%s declares storage (%s) and nothing says what it holds.

%s owns it, and its declaration has no holds entry with "held_by": %q. Until
it does the safety officer reads it as holding nothing, and clears a teardown
that takes whatever is on it. Say what it is, the scope it is worth keeping
for the life of, and the scope its working copy dies with.`, k.file, k.object, k.owner, k.object)
		}
	}
	// And where each says it lives is where its storage puts it. A volume
	// on a class the storage driver provides is kept on the node and
	// outlives any machine; a volume on any other class is on a machine's
	// own disk and goes with it. A declaration that says otherwise is the
	// one the safety officer would believe.
	kept := classesThatOutliveAMachine(t)
	livesOn := map[string]map[string]string{holds.Core: {}}
	for _, a := range core {
		livesOn[holds.Core][a.HeldBy] = a.LivesOn
	}
	for _, app := range apps {
		livesOn[app.Name] = map[string]string{}
		for _, a := range app.Holds {
			livesOn[app.Name][a.HeldBy] = a.LivesOn
		}
	}
	for _, k := range keeps {
		said, ok := livesOn[k.owner][k.object]
		if !ok || len(k.classes) == 0 {
			continue
		}
		for _, class := range k.classes {
			is := holds.Machine.String()
			if kept[class] {
				is = holds.Node.String()
			}
			if said != is {
				t.Errorf(`%s keeps %s on the class %q, which lives on a %s, and %s declares it lives on a %s.

The safety officer decides whether destroying a machine endangers it from
that declaration. Said to live on a node when it is on a machine's disk, it is
cleared for a retirement that takes it; the other way round, a retirement is
refused over something that was never at risk.`, k.file, k.object, class, is, k.owner, said)
			}
		}
	}

	for owner, objects := range declared {
		for object := range objects {
			if !found[owner][object] {
				t.Errorf("%s says it holds something in %s, and nothing of its own declares storage by that kind and name", owner, object)
			}
		}
	}
}

// keeper is one object that declares storage, whose it is, and every
// storage class it names.
type keeper struct {
	file, owner, object string
	classes             []string
}

// classesThatOutliveAMachine is the classes the storage driver provides: the
// ones its release declares, found by what declares them. Any other class in
// the estate hands out a machine's own disk.
func classesThatOutliveAMachine(t *testing.T) map[string]bool {
	t.Helper()
	_, body := fluxObject(t, kindHelmRelease, "storage-driver")
	var release struct {
		Spec struct {
			Values struct {
				StorageClass []struct {
					Name string `yaml:"name"`
				} `yaml:"storageClass"`
			} `yaml:"values"`
		} `yaml:"spec"`
	}
	dec := yaml.NewDecoder(strings.NewReader(body))
	out := map[string]bool{}
	for {
		err := dec.Decode(&release)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("the storage driver's release does not parse: %v", err)
		}
		for _, c := range release.Spec.Values.StorageClass {
			out[c.Name] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("the storage driver's release declares no class, so nothing could be said to outlive a machine")
	}
	return out
}

// classesNamed is every storage class a manifest names, at any depth and
// under either spelling charts and operators use.
func classesNamed(n *yaml.Node, into *[]string) {
	if n == nil {
		return
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, value := n.Content[i].Value, n.Content[i+1]
			if (key == "storageClassName" || key == "storageClass") && value.Kind == yaml.ScalarNode && value.Value != "" {
				*into = append(*into, value.Value)
			}
		}
	}
	for _, c := range n.Content {
		classesNamed(c, into)
	}
}

// storageDeclared is every object in the repository's own manifests that
// asks for a volume: a claim, anything carrying a claim template at any
// depth (a StatefulSet, or a chart's values asking for one), and a database
// cluster that says where its data goes.
func storageDeclared(t *testing.T) []keeper {
	t.Helper()
	tracked := trackedFiles(t)
	vendored := vendoredFiles(t, tracked)
	var out []keeper
	for _, rel := range tracked {
		if vendored[rel] || !strings.HasSuffix(rel, ".yaml") && !strings.HasSuffix(rel, ".yml") || strings.HasPrefix(rel, ".github/") || testMaterial.MatchString(rel) {
			continue
		}
		owner := holds.Core
		if rest, ok := strings.CutPrefix(rel, applications.Dir+"/"); ok {
			owner, _, _ = strings.Cut(rest, "/")
		}
		dec := yaml.NewDecoder(strings.NewReader(readRepoFile(t, rel)))
		for {
			var doc yaml.Node
			err := dec.Decode(&doc)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("%s does not parse as YAML, so what it keeps was not read: %v", rel, err)
			}
			if len(doc.Content) == 0 {
				continue
			}
			top := doc.Content[0]
			kind, md := mappingValue(top, "kind"), mappingValue(top, "metadata")
			if kind == nil || md == nil || mappingValue(md, "name") == nil {
				continue
			}
			keeps := kind.Value == "PersistentVolumeClaim" ||
				kind.Value == "Cluster" && mappingValue(mappingValue(top, "spec"), "storage") != nil ||
				hasKey(top, "volumeClaimTemplate", "volumeClaimTemplates")
			if keeps {
				var classes []string
				classesNamed(top, &classes)
				out = append(out, keeper{rel, owner, kind.Value + "/" + mappingValue(md, "name").Value, classes})
			}
		}
	}
	return out
}

// hasKey says whether any mapping at any depth carries one of the keys.
func hasKey(n *yaml.Node, keys ...string) bool {
	if n == nil {
		return false
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			for _, k := range keys {
				if n.Content[i].Value == k {
					return true
				}
			}
		}
	}
	for _, c := range n.Content {
		if hasKey(c, keys...) {
			return true
		}
	}
	return false
}

// Nothing tears a site down without the safety officer clearing it first.
//
// The officer is another program, so that the one holding the detonator
// cannot be what decides the explosion is safe. That only holds while every
// path to the teardown goes past it, and the next path added is the one that
// will not. So this reads the source: any function that starts a teardown
// asks the officer earlier in its own body.
//
// One path is not asked, and it is not a teardown of a site: a build that
// fails undoes what that same run built, minutes old, before anything on it
// was worth keeping. Asking there would refuse over copies belonging to a
// site that stood before, and leave the half-built machines behind.
func TestNoTeardownStartsUntilTheSafetyOfficerClearsIt(t *testing.T) {
	const teardown, officer, undoesItsOwnBuild = "tearDown", "clearedBySafetyOfficer", "EmergencyDestroy"

	root := repoRoot(t)
	var starts, asked, exempt int
	for _, rel := range goFiles(t) {
		if strings.HasSuffix(rel, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, rel), nil, 0)
		if err != nil {
			t.Fatalf("%s does not parse, so its teardowns were not read: %v", rel, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			var tornDown, cleared []token.Pos
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if id, ok := call.Fun.(*ast.Ident); ok {
					switch id.Name {
					case teardown:
						tornDown = append(tornDown, call.Pos())
					case officer:
						cleared = append(cleared, call.Pos())
					}
				}
				return true
			})
			if len(tornDown) == 0 {
				continue
			}
			starts++
			if fn.Name.Name == undoesItsOwnBuild {
				exempt++
				continue
			}
			sort.Slice(tornDown, func(i, j int) bool { return tornDown[i] < tornDown[j] })
			if len(cleared) == 0 || cleared[0] > tornDown[0] {
				t.Errorf(`%s: %s starts a teardown without asking the safety officer first.

Whatever this destroys may hold the only copy of something that should outlive
it, and the officer is the only party that looks. Call %s before %s, and
return what it returns.`, rel, fn.Name.Name, officer, teardown)
				continue
			}
			asked++
		}
	}
	if starts == 0 || asked == 0 {
		t.Fatalf("found %d function(s) that start a teardown and %d that ask the officer first, so this is no longer reading the teardown", starts, asked)
	}
	if exempt > 1 {
		t.Errorf("%d functions are named %s; one build undoes itself, and no more", exempt, undoesItsOwnBuild)
	}
}

// The storage driver's token is confined by two names, and each name is
// written twice: once where the hypervisor is told what to grant, once where
// OpenTofu makes the thing. If the two drift, the token is confined to a
// pool with no machines in it, or the driver is pointed at a storage it has
// no right to - and each half is correct when read alone.
func TestTheStorageDriversNamesAreTheSameWhereverTheyAreWritten(t *testing.T) {
	workers := (&config.SiteNetwork{Name: "${local.site_name}"}).WorkerPool()
	if _, body := tofuDeclaring(t, `resource "proxmox_virtual_environment_pool" "workers"`); !strings.Contains(body, `"`+workers+`"`) {
		t.Errorf("the pool a site's workers are put in is not %q, which is the pool the hypervisor confines the storage driver's token to", workers)
	}
	volumes := config.VolumeStorage("${var.site}")
	if _, body := tofuDeclaring(t, `resource "kubernetes_secret" "storage_vars"`); !strings.Contains(body, `"`+volumes+`"`) {
		t.Errorf("the storage the driver is told to keep volumes in is not %q, which is the one the hypervisor makes for it and grants it", volumes)
	}
}

// What each machine is given is written twice until a site's config says
// it: where the cluster module makes the machine, and where the contractor
// budgets a hypervisor's memory before anything is built. If the two drift,
// the budget passes a site its host cannot hold, or refuses one it can.
func TestAMachinesMemoryIsTheSameWhereItIsMadeAndWhereItIsBudgeted(t *testing.T) {
	given := regexp.MustCompile(`dedicated\s*=\s*(\d+)`)
	for resource, role := range map[string]config.MachineRole{
		"talos_cp": config.ControlPlane, "talos_worker": config.Worker, "dmz": config.Untrusted,
	} {
		_, body := tofuDeclaring(t, `resource "proxmox_virtual_environment_vm" "`+resource+`"`)
		_, block, found := strings.Cut(body, `resource "proxmox_virtual_environment_vm" "`+resource+`"`)
		if !found {
			t.Fatalf("the machine %s is not declared where it was found", resource)
		}
		if next := strings.Index(block, "\nresource "); next >= 0 {
			block = block[:next]
		}
		m := given.FindStringSubmatch(block)
		if m == nil {
			t.Fatalf("the machine %s is given no memory this can read", resource)
		}
		mebibytes, _ := strconv.ParseInt(m[1], 10, 64)
		if made, budgeted := mebibytes<<20, config.MemoryOf[role]; made != budgeted {
			t.Errorf("a %s is made with %d MiB and budgeted at %d MiB. The budget is what refuses a site its host cannot hold, and it is holding the wrong figure", role, made>>20, budgeted>>20)
		}
	}
}

// A second hypervisor reopens how idle capacity is lent.
//
// The decision was put off on 2026-10-08, with two things that would reopen
// it. One is seen in the cluster and is an alert
// (LendingIdleCapacityNeedsAccounting). The other is seen here: a site given
// a second hypervisor. With one host, every machine shares the same memory
// and lending what is idle is a matter of who is stopped first. With two,
// where work that can wait is put starts to matter, and that is the
// accounting Koordinator does and Kubernetes unaided does not.
//
// This fails on the change that adds the second hypervisor, so the question
// is asked by whoever is adding it and not remembered by anybody. It is
// answered by changing this test: to the decision that was made.
func TestASecondHypervisorReopensHowIdleCapacityIsLent(t *testing.T) {
	// Read as the program reads it, so this is not a second idea of what
	// the config holds.
	var template config.Config
	if err := json.Unmarshal([]byte(readRepoFile(t, "config/management.tpl.json")), &template); err != nil {
		t.Fatalf("the config template is not JSON this reads: %v", err)
	}
	if len(template.Sites) == 0 {
		t.Fatal("the config template declares no site, so this looked at nothing")
	}
	for name, site := range template.Sites {
		if len(site.Hypervisor.Nodes) == 0 {
			t.Errorf("the site %s declares no hypervisor, so this looked at nothing for it", name)
		}
		if len(site.Hypervisor.Nodes) > 1 {
			t.Errorf(`the site %s has %d hypervisors, which reopens a decision that was put off.

Idle capacity is lent with no accounting: work that can wait reserves little,
runs in what is free and is stopped when the memory is wanted back. That was
chosen for a site with one host. With a second, where such work is placed
matters, and two projects that account for it were looked at and not taken up:
Koordinator, which is maintained, and Crane, which is not and whose prediction
of a workload's cycles is still the part worth reading.

Decide whether to trial Koordinator before this site grows, then change this
test to say what was decided. What was found is beside the alert
LendingIdleCapacityNeedsAccounting.`, name, len(site.Hypervisor.Nodes))
		}
	}
}
