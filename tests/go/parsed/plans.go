package parsed

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"homelab/contractor/config"
	"homelab/contractor/steps"
	"homelab/details/applications"
	"homelab/details/asbuilt"
	"homelab/details/platform"
	"homelab/details/repopath"
)

// UnbuiltPlans is every site's roots planned from nothing, as files a policy
// engine is handed: one plan as `tofu show -json` writes it per site and
// root, made with the real providers and a stand-in for every value the
// vault supplies (steps.PlanUnbuilt). The sites are the ones the config
// template declares. A root that does not plan fails the test: a rule over
// a plan that was never made would judge nothing and pass. It fails it and
// goes on, with the plans that were made: a change that stops a root
// planning is often one a rule over the files refuses by name, and a plan
// that failed must not be what hides that.
//
// A plan takes seconds, and the mutation ledger judges a copy of the tree
// for each of its proofs, most of which change nothing a plan is made from.
// So a plan is kept by what it was made from - every file under the roots,
// the modules, the applications and the config, and the site's own list of
// applications - and a tree that holds exactly those files is handed the
// same plan again.
func (r *Tree) UnbuiltPlans(t testing.TB) []string {
	t.Helper()
	sites, err := config.DeclaredSites(r.path("config/management.tpl.json"))
	if err != nil {
		t.Fatalf("reading which sites the config template declares: %v", err)
	}
	if len(sites) == 0 {
		t.Fatal("the config template declares no site, so there is nothing to plan")
	}
	out := t.TempDir()
	// Each plan is written under the scope its root is: a site's roots under
	// site/, every other root under estate/. A node has no root of its own,
	// so a root that is not a site's is the estate's, and a rule tells the
	// two apart by where the plan is (lib.plan_scope).
	for _, scope := range []string{siteScope, estateScope} {
		if err := os.MkdirAll(filepath.Join(out, scope), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var files []string
	for _, site := range sites {
		kept, err := r.keptPlans(t, site, steps.Released{})
		if err != nil {
			t.Errorf("planning %s from nothing: %v", site, err)
			continue
		}
		for _, root := range kept {
			if root.Err != nil {
				t.Errorf("the %s root of %s does not plan from nothing, so no rule over its plan can be judged: %v", root.Root, site, root.Err)
				continue
			}
			if len(root.Plan) == 0 {
				t.Errorf("the %s root of %s planned to nothing, so no rule over its plan can be judged", root.Root, site)
				continue
			}
			file := filepath.Join(out, siteScope, site+"-"+root.Root+".plan.json")
			if err := os.WriteFile(file, root.Plan, 0o600); err != nil {
				t.Fatal(err)
			}
			files = append(files, file)
		}
	}
	files = append(files, r.pinnedPlans(t, out, sites)...)
	return append(files, r.wholePlans(t, out)...)
}

// pinnedPlans is each site's roots planned from nothing with the modules of
// the release its line in the versions file names, under pinned/: what a
// converge of the site applies once this merges, before its line moves. Flux
// applies what merges at once and the site's own secrets are written by the
// release it runs, so what Flux applies is held to these too (#843).
//
// The release's files are read from the checkout this package was built in,
// which holds the tags: a copy of the tree holds none of its history.
func (r *Tree) pinnedPlans(t testing.TB, out string, sites []string) []string {
	t.Helper()
	objects, err := repopath.Root()
	if err != nil {
		t.Fatal(err)
	}
	pins, err := platform.Pins(r.root)
	if err != nil {
		t.Fatalf("reading which release each site runs: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(out, pinnedScope), 0o700); err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, site := range sites {
		pin, ok := pins[site]
		if !ok {
			t.Errorf("%s is a site and runs no release, so what it runs could not be planned", site)
			continue
		}
		revision, err := platform.Revision(objects, pin)
		if err != nil {
			t.Errorf("%s runs %s: %v", site, pin.Version, err)
			continue
		}
		kept, err := r.keptPlans(t, site, steps.Released{Objects: objects, Revision: revision})
		if err != nil {
			t.Errorf("planning %s from nothing with the modules of %s: %v", site, pin.Version, err)
			continue
		}
		for _, root := range kept {
			if root.Err != nil {
				t.Errorf("the %s root of %s does not plan from nothing with the modules of %s, the release it runs: %v", root.Root, site, pin.Version, root.Err)
				continue
			}
			file := filepath.Join(out, pinnedScope, site+"-"+root.Root+".plan.json")
			if err := os.WriteFile(file, root.Plan, 0o600); err != nil {
				t.Fatal(err)
			}
			files = append(files, file)
		}
	}
	return files
}

// The directories a plan is written under, by the scope of the root it is of:
// the two scopes that have roots. policy/conftest/lib reads them back.
const (
	siteScope   = "site"
	estateScope = "estate"
	// pinnedScope is a site's roots planned with the modules of the release
	// it runs, beside the checkout's own.
	pinnedScope = "pinned"
)

// wholePlans is every root that is not a site's, planned whole from nothing
// (asbuilt.PlanWhole): the estate's, which keeps its modules beside it. A
// root is found by what it is (OpenTofuRoots), and its config template by
// what it reads - the default of its config_path, rendered from the template
// of the same name - so a root added later is planned without being listed,
// and one with no template to plan from fails.
func (r *Tree) wholePlans(t testing.TB, out string) []string {
	t.Helper()
	site := map[string]bool{}
	for _, name := range config.Roots {
		site["management/"+name] = true
	}
	tracked := r.Files(t)
	var files []string
	for _, root := range OpenTofuRoots(r.OpenTofu(t)) {
		if site[root] {
			continue
		}
		template, ok := r.templateOf(t, root)
		if !ok {
			t.Errorf("%s is a root and not a site's, and declares no config_path whose template it can be planned from, so no rule over its plan can be judged", root)
			continue
		}
		plan, err := r.keptWhole(t, root, template, tracked)
		if err != nil {
			t.Errorf("%s does not plan from nothing, so no rule over its plan can be judged: %v", root, err)
			continue
		}
		file := filepath.Join(out, estateScope, path.Base(root)+".plan.json")
		if err := os.WriteFile(file, plan, 0o600); err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	return files
}

// templateOf is the config template a root is filled from: the default of
// its config_path, the rendered file named for the template it is rendered
// from.
func (r *Tree) templateOf(t testing.TB, root string) (string, bool) {
	t.Helper()
	for _, b := range r.Blocks(t, "variable", "config_path") {
		if b.Dir() != root {
			continue
		}
		def, ok := b.Body.Attributes["default"]
		if !ok {
			return "", false
		}
		v, ok := Literal(def.Expr)
		if !ok || v.IsNull() || v.Type() != cty.String || !strings.Contains(v.AsString(), ".rendered.") {
			return "", false
		}
		return path.Join(root, strings.Replace(v.AsString(), ".rendered.", ".tpl.", 1)), true
	}
	return "", false
}

// keptWhole is a root's plan from nothing: the one made before from exactly
// these files, or a new one, made under the same lock as a site's.
func (r *Tree) keptWhole(t testing.TB, root, template string, tracked []string) ([]byte, error) {
	key, err := r.planKey(t, path.Base(root), "")
	if err != nil {
		return nil, err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	plans := filepath.Join(cache, "homelab", "unbuilt-plans")
	kept := filepath.Join(plans, key, "whole.json")
	if plan, err := os.ReadFile(kept); err == nil {
		return plan, nil
	}
	if err := os.MkdirAll(plans, 0o700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(plans, "planning.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	if plan, err := os.ReadFile(kept); err == nil {
		return plan, nil
	}
	body, err := os.ReadFile(r.path(template))
	if err != nil {
		return nil, err
	}
	var files []string
	for _, rel := range tracked {
		if inner, ok := strings.CutPrefix(rel, root+"/"); ok {
			files = append(files, inner)
		}
	}
	// The copy two directories below the top, as the root is, in a
	// workspace of its own that git ignores.
	top, err := os.MkdirTemp(r.root, ".as-built-unbuilt-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(top) }()
	plan, err := asbuilt.PlanWhole(asbuilt.WholeInputs{
		Root: r.path(root), Files: files, Work: filepath.Join(top, path.Base(root)),
		Template: body, Providers: filepath.Join(plans, "providers"),
	}, asbuilt.Exec)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(kept), 0o700); err != nil {
		return nil, err
	}
	return plan, os.WriteFile(kept, plan, 0o600)
}

// keptPlans is a site's plans: the ones made before from exactly these
// files, or new ones, kept for the next tree that holds them.
//
// ONE PLAN AT A TIME, across every process on the machine. The ledger runs
// its proofs side by side, and every one that starts with no plan kept would
// plan at once, each fetching every provider afresh: the registry refuses a
// crowd, and a proof refused it fails for a reason that is not its own. So a
// plan is made under a lock, and a tree that waited for one looks again for
// what the plan before it kept; and the providers one plan fetched are kept
// for the next, by the lock file that chose them, so a machine reaches the
// registry once for each set of providers rather than once for each plan.
func (r *Tree) keptPlans(t testing.TB, site string, release steps.Released) ([]steps.Unbuilt, error) {
	key, err := r.planKey(t, site, release.Revision)
	if err != nil {
		return nil, err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	plans := filepath.Join(cache, "homelab", "unbuilt-plans")
	dir := filepath.Join(plans, key)
	if kept, err := readKept(dir); err != nil || kept != nil {
		return kept, err
	}
	if err := os.MkdirAll(plans, 0o700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(plans, "planning.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	if kept, err := readKept(dir); err != nil || kept != nil {
		return kept, err
	}
	made, err := steps.PlanUnbuiltAt(r.root, site, filepath.Join(plans, "providers"), release)
	if err != nil {
		return nil, err
	}
	for _, root := range made {
		if root.Err != nil {
			return made, nil
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	for _, root := range made {
		if err := os.WriteFile(filepath.Join(dir, root.Root+".json"), root.Plan, 0o600); err != nil {
			return nil, err
		}
	}
	return made, nil
}

// readKept is the plans kept in a directory, one per root, or none when any
// root's is not there: a plan of some of a site's roots is not its plan.
func readKept(dir string) ([]steps.Unbuilt, error) {
	var kept []steps.Unbuilt
	for _, root := range []string{config.ClusterRoot, config.PlatformRoot} {
		plan, err := os.ReadFile(filepath.Join(dir, root+".json"))
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil // not made from these files before
		}
		if err != nil {
			return nil, err
		}
		kept = append(kept, steps.Unbuilt{Root: root, Plan: plan})
	}
	return kept, nil
}

// planKey is what a site's plans are made from, as one digest: every
// tracked file under the roots, the modules, the applications and the
// config, every file the release holds - which is every file a module reads
// from outside its own directory - and the site's own list of applications,
// by path and content.
//
// Planned with a release's modules, the release's commit stands for the
// modules and what they read, and the checkout's are left out: the roots,
// the config and the site's list are what the checkout gives.
func (r *Tree) planKey(t testing.TB, site, revision string) (string, error) {
	release, err := platform.Read(r.root)
	if err != nil {
		return "", err
	}
	var from []string
	for _, rel := range r.Files(t) {
		roots := strings.HasPrefix(rel, "management/") || strings.HasPrefix(rel, "config/") || rel == applications.SiteFilePath(site)
		modules := strings.HasPrefix(rel, "modules/") || platform.Holds(release.Holds, rel)
		if roots || modules && revision == "" {
			from = append(from, rel)
		}
	}
	sort.Strings(from)
	h := sha256.New()
	_, _ = io.WriteString(h, site+"\x00")
	// Only where there is a release, so the checkout's own plans keep the
	// keys they were kept by.
	if revision != "" {
		_, _ = io.WriteString(h, "release\x00"+revision+"\x00")
	}
	for _, rel := range from {
		body, err := os.ReadFile(r.path(rel))
		if err != nil {
			return "", err
		}
		_, _ = io.WriteString(h, rel+"\x00")
		_, _ = h.Write(body)
		_, _ = io.WriteString(h, "\x00")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
