package parsed

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"homelab/contractor/config"
	"homelab/contractor/steps"
	"homelab/details/applications"
)

// UnbuiltPlans is every site's roots planned from nothing, as files a policy
// engine is handed: one plan as `tofu show -json` writes it per site and
// root, made with the real providers and a stand-in for every value the
// vault supplies (steps.PlanUnbuilt). The sites are the ones the config
// template declares. A root that does not plan fails the test: a rule over
// a plan that was never made would judge nothing and pass.
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
	var files []string
	for _, site := range sites {
		kept, err := r.keptPlans(t, site)
		if err != nil {
			t.Fatal(err)
		}
		for _, root := range kept {
			if root.Err != nil {
				t.Fatalf("the %s root of %s does not plan from nothing, so no rule over its plan can be judged: %v", root.Root, site, root.Err)
			}
			if len(root.Plan) == 0 {
				t.Fatalf("the %s root of %s planned to nothing, so no rule over its plan can be judged", root.Root, site)
			}
			file := filepath.Join(out, site+"-"+root.Root+".plan.json")
			if err := os.WriteFile(file, root.Plan, 0o600); err != nil {
				t.Fatal(err)
			}
			files = append(files, file)
		}
	}
	return files
}

// keptPlans is a site's plans: the ones made before from exactly these
// files, or new ones, kept for the next tree that holds them.
func (r *Tree) keptPlans(t testing.TB, site string) ([]steps.Unbuilt, error) {
	key, err := r.planKey(t, site)
	if err != nil {
		return nil, err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(cache, "homelab", "unbuilt-plans", key)
	roots := []string{config.ClusterRoot, config.PlatformRoot}
	var kept []steps.Unbuilt
	for _, root := range roots {
		plan, err := os.ReadFile(filepath.Join(dir, root+".json"))
		if errors.Is(err, fs.ErrNotExist) {
			break // not made from these files before
		}
		if err != nil {
			return nil, err
		}
		kept = append(kept, steps.Unbuilt{Root: root, Plan: plan})
	}
	if len(kept) == len(roots) {
		return kept, nil
	}
	made, err := steps.PlanUnbuilt(r.root, site)
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

// planKey is what a site's plans are made from, as one digest: every
// tracked file under the roots, the modules, the applications and the
// config, and the site's own list of applications, by path and content.
func (r *Tree) planKey(t testing.TB, site string) (string, error) {
	var from []string
	for _, rel := range r.Files(t) {
		if strings.HasPrefix(rel, "management/") || strings.HasPrefix(rel, "modules/") ||
			strings.HasPrefix(rel, "config/") || rel == applications.SiteFilePath(site) {
			from = append(from, rel)
		}
	}
	sort.Strings(from)
	h := sha256.New()
	_, _ = io.WriteString(h, site+"\x00")
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
