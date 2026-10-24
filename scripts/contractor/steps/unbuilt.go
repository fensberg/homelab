package steps

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"homelab/contractor/config"
	"homelab/details/asbuilt"
	"homelab/details/platform"
)

// unbuiltWork begins the name of each plan's own workspace, at the top of
// the checkout, which git ignores; its copy of the platform is
// .unreleased with the same ending.
const unbuiltWork = ".as-built-unbuilt"

// Unbuilt is one root's plan from nothing: the plan as `tofu show -json`
// writes it, or why the root would not plan.
type Unbuilt struct {
	Root string
	Plan []byte
	Err  error
}

// PlanUnbuilt plans each of a site's roots from nothing, as the first build
// would apply it, step by step as the converge walks them: the real
// providers, the repository's own template with the site's applications
// added, and a stand-in for every value the vault supplies. It needs no
// record, no state and no credential, so it holds for a root nobody has
// built yet and for a resource the change in hand adds.
//
// The modules planned are what a release of the platform would hold from
// this checkout, and nothing else of the repository: a file a module reads
// that the release does not hold is not there, and the plan fails on it.
//
// Providers is where each root's providers are kept between plans, by the
// lock file that chose them: a root plans from what is kept there, and keeps
// what it fetched when nothing was. Empty, a root plans from what its own
// initialised directory holds, or fetches.
//
// An error is returned for what stops every root being planned; a root that
// does not plan says so in its own Err.
func PlanUnbuilt(repo, site, providers string) ([]Unbuilt, error) {
	committed, err := os.ReadFile(filepath.Join(repo, "config", "management.tpl.json"))
	if err != nil {
		return nil, err
	}
	tpl, err := config.Compose(committed, repo, site)
	if err != nil {
		return nil, err
	}
	// proved by TestEveryProgramRunIsNamedByAConstantOrIsTheOperators
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	tracked, err := exec.Command("git", "-C", repo, "ls-files", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("listing the tracked files: %w", err)
	}
	// Each plan's own copy of the platform and its own workspace, under names
	// of its own at the top of the checkout: what a plan reads must not be
	// pulled away by anything else using the checkout's, and a test of
	// another package does. The copy of a root is made two directories below
	// the top, as the root is, so its references up the tree still resolve.
	work, err := os.MkdirTemp(repo, unbuiltWork+"-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(work) }()
	unreleased := platform.Unreleased + "-" + filepath.Base(work)[len(unbuiltWork)+1:]
	if _, err := platform.PlaceUnreleasedAs(repo, strings.Split(strings.TrimRight(string(tracked), "\x00"), "\x00"), unreleased); err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(filepath.Join(repo, unreleased)) }()
	records, err := os.MkdirTemp("", "unbuilt-record-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(records) }()

	var out []Unbuilt
	for _, root := range []string{config.ClusterRoot, config.PlatformRoot} {
		dir := filepath.Join(repo, "management", root)
		record := filepath.Join(records, root)
		if err := asbuilt.WriteUnbuilt(record, site); err != nil {
			return nil, err
		}
		in := asbuilt.PlanInputs{
			Root: dir, Work: work, Record: record,
			Template: tpl, Site: site,
			Sequence: Plan(root),
		}
		// A machine that has initialised the root plans from what it holds.
		if cache := filepath.Join(dir, ".terraform", "providers"); isDir(cache) {
			in.PluginDir = cache
		}
		kept := ""
		if providers != "" {
			lock, err := os.ReadFile(filepath.Join(dir, asbuilt.ProviderLock))
			if err != nil {
				return nil, fmt.Errorf("the %s root has no provider lock to keep its providers by: %w", root, err)
			}
			kept = filepath.Join(providers, root, fmt.Sprintf("%x", sha256.Sum256(lock)))
			if isDir(kept) {
				in.PluginDir = kept
			}
		}
		in.Vars = map[string]string{}
		if root == config.PlatformRoot {
			in.Vars = asbuilt.UnbuiltAccess(PlatformInputs[0], PlatformInputs[1])
		}
		// From the copy of the root the plan is made in, which is two
		// directories down from the top of the repository as the root is.
		in.Vars[strings.TrimPrefix(platform.UnreleasedVariable, "TF_VAR_")] = "../../" + unreleased
		plan, _, err := asbuilt.PlanAgainst(in, asbuilt.Exec)
		if err == nil && kept != "" && in.PluginDir == "" {
			if err := os.CopyFS(kept, os.DirFS(filepath.Join(in.Work, "plan", ".terraform", "providers"))); err != nil {
				return nil, fmt.Errorf("keeping the %s root's providers: %w", root, err)
			}
		}
		_ = os.RemoveAll(in.Work)
		out = append(out, Unbuilt{Root: root, Plan: plan, Err: err})
	}
	return out, nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
