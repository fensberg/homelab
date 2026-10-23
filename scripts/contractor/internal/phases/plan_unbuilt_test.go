package phases

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"homelab/contractor/config"
	"homelab/contractor/steps"
	"homelab/details/asbuilt"
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
	const site = "site0"
	_, given, err := config.SiteApplications(repo, site)
	if err != nil {
		t.Fatal(err)
	}
	plans, err := steps.PlanUnbuilt(repo, site)
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range plans {
		plan, err := root.Plan, root.Err
		var keyed *asbuilt.VaultKeyError
		if errors.As(err, &keyed) {
			t.Errorf("the %s root: %v", root.Root, err)
			continue
		}
		if err != nil {
			t.Errorf("the %s root does not plan from nothing: %v", root.Root, err)
			continue
		}
		// A plan that plans nothing proves none of the above.
		pending, err := asbuilt.Pending(plan)
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) < 5 {
			t.Errorf("the %s root's plan from nothing would create %d thing(s) (%s), so it is not a plan of that root", root.Root, len(pending), strings.Join(pending, ", "))
		}
		// A site that was given applications has their namespaces, secrets
		// and identities in the platform's plan, and one given none has
		// none of them. The plan names kinds and never keys, so this asks
		// whether they are there, not how many.
		if root.Root == config.PlatformRoot {
			for _, kind := range []string{"kubernetes_namespace.application", "kubernetes_service_account.application_reconciler", "kubernetes_role_binding.application_reconciler"} {
				if planned := slices.Contains(pending, kind); planned != (len(given) > 0) {
					t.Errorf("the site was given %d application(s), and %s being in the platform's plan from nothing is %v (%s)", len(given), kind, planned, strings.Join(pending, ", "))
				}
			}
		}
	}
}
