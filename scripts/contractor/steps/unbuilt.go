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

// Unbuilt is one root's plan from nothing: the plan as `tofu show -json`
// writes it, or why the root would not plan.
type Unbuilt struct {
	Root string
	Plan []byte
	Err  error
}

// unbuiltWork is where the plans are made: beside the Record phase's own
// workspace, never in it, because a run on this machine may be using that.
// One level below the top of the repository, as a root is two, so the copy
// of a root made inside it reaches the unreleased platform as the root would.
const unbuiltWork = ".as-built-unbuilt"

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
	unreleased, err := platform.PlaceUnreleased(repo, strings.Split(strings.TrimRight(string(tracked), "\x00"), "\x00"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(unreleased) }()
	records, err := os.MkdirTemp("", "unbuilt-record-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(records) }()

	work := filepath.Join(repo, unbuiltWork)
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
		in.Vars[strings.TrimPrefix(platform.UnreleasedVariable, "TF_VAR_")] = "../../" + platform.Unreleased
		plan, _, err := asbuilt.PlanAgainst(in, asbuilt.Exec)
		if err == nil && kept != "" && in.PluginDir == "" {
			if err := os.CopyFS(kept, os.DirFS(filepath.Join(work, "plan", ".terraform", "providers"))); err != nil {
				return nil, fmt.Errorf("keeping the %s root's providers: %w", root, err)
			}
		}
		_ = os.RemoveAll(work)
		out = append(out, Unbuilt{Root: root, Plan: plan, Err: err})
	}
	return out, nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
