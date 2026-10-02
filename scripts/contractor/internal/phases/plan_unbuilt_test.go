package phases

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"homelab/contractor/config"
	"homelab/contractor/internal/run"
	"homelab/contractor/steps"
	"homelab/details/asbuilt"
	"homelab/details/platform"
	"homelab/details/repopath"
)

// Both of a site's roots plan from nothing, with the real providers and a
// stand-in for every value the vault supplies, and no resource in either
// plan is keyed by one of those values.
//
// WHY THIS EXISTS. Nothing planned a machine, a template or a namespace before
// a merge except the plan against the as-built record - and that needs a
// record of the shape the change has. The change that split the site into two
// roots had none, so the largest change the roots have had was planned by
// nothing, and the check that refuses a resource keyed by a vault value had
// never run on the change that introduced it.
//
// This needs no record. It plans each root as the first build would apply it,
// step by step as the converge walks them, from the repository's own template.
// So it holds for a root nobody has built yet, and for a resource the change
// in hand adds.
//
// It proves three things a reading of the source cannot: that the root is
// accepted by the providers it is written for; that every step of the
// converge is a plan those providers will make; and that no instance key
// anywhere - a for_each, a count, a module - carries a vault value.
func TestEveryRootPlansFromNothingAndKeysNoResourceByAVaultValue(t *testing.T) {
	if testing.Short() {
		t.Skip("plans both roots with the real providers, which the full run does")
	}
	repo, err := repopath.Root()
	if err != nil {
		t.Fatal(err)
	}
	ctx := run.NewContext(repo, "site0")
	committed, err := os.ReadFile(ctx.ConfigTpl)
	if err != nil {
		t.Fatal(err)
	}
	// The template a run reads: the committed one with the site's
	// applications added, so that what each of them declares - its
	// namespace, its secrets, its identity - is planned by the real provider
	// too, whichever applications the site was given.
	tpl, err := config.Compose(committed, repo, ctx.Site)
	if err != nil {
		t.Fatal(err)
	}
	_, given, err := config.SiteApplications(repo, ctx.Site)
	if err != nil {
		t.Fatal(err)
	}
	// Beside the Record phase's own workspace, never in it: a run on this
	// machine may be using that.
	// What a release of the platform would hold from this checkout, and
	// nothing else of the repository: this is the check that sees a change
	// to a module before it merges, and it sees it as a site would run it.
	// A file a module reads that the release does not hold is not there, and
	// the plan fails on it here.
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	tracked, err := exec.Command("git", "-C", repo, "ls-files", "-z").Output()
	if err != nil {
		t.Fatal(err)
	}
	unreleased, err := platform.PlaceUnreleased(repo, strings.Split(strings.TrimRight(string(tracked), "\x00"), "\x00"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(unreleased) })
	work := filepath.Join(repo, ".as-built-unbuilt")
	t.Cleanup(func() { _ = os.RemoveAll(work) })

	for _, root := range ctx.Roots() {
		record := filepath.Join(t.TempDir(), root.Name)
		if err := asbuilt.WriteUnbuilt(record, ctx.Site); err != nil {
			t.Fatal(err)
		}
		in := asbuilt.PlanInputs{
			Root: root.Dir, Work: work, Record: record,
			Template: tpl, Site: ctx.Site,
			Sequence: steps.Plan(root.Name),
		}
		// A machine that has initialised the root plans from what it holds.
		if cache := filepath.Join(root.Dir, ".terraform", "providers"); isDir(cache) {
			in.PluginDir = cache
		}
		in.Vars = map[string]string{}
		if root.Name == config.PlatformRoot {
			in.Vars = asbuilt.UnbuiltAccess(steps.PlatformInputs[0], steps.PlatformInputs[1])
		}
		// From the copy of the root the plan is made in, which is two
		// directories down from the top of the repository as the root is.
		in.Vars[strings.TrimPrefix(platform.UnreleasedVariable, "TF_VAR_")] = "../../" + platform.Unreleased
		plan, _, err := asbuilt.PlanAgainst(in, asbuilt.Exec)
		_ = os.RemoveAll(work)

		var keyed *asbuilt.VaultKeyError
		if errors.As(err, &keyed) {
			t.Errorf("the %s root: %v", root.Name, err)
			continue
		}
		if err != nil {
			t.Errorf("the %s root does not plan from nothing: %v", root.Name, err)
			continue
		}
		// A plan that plans nothing proves none of the above.
		pending, err := asbuilt.Pending(plan)
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) < 5 {
			t.Errorf("the %s root's plan from nothing would create %d thing(s) (%s), so it is not a plan of that root", root.Name, len(pending), strings.Join(pending, ", "))
		}
		// A site that was given applications has their namespaces, secrets
		// and identities in the platform's plan, and one given none has
		// none of them. The plan names kinds and never keys, so this asks
		// whether they are there, not how many.
		if root.Name == config.PlatformRoot {
			for _, kind := range []string{"kubernetes_namespace.application", "kubernetes_service_account.application_reconciler", "kubernetes_role_binding.application_reconciler"} {
				if planned := slices.Contains(pending, kind); planned != (len(given) > 0) {
					t.Errorf("the site was given %d application(s), and %s being in the platform's plan from nothing is %v (%s)", len(given), kind, planned, strings.Join(pending, ", "))
				}
			}
		}
	}
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
