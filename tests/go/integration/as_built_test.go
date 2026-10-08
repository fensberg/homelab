//go:build integration

package integration_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gruntwork-io/terratest/modules/terraform"

	"homelab/contractor/config"
	"homelab/contractor/steps"
	"homelab/details/asbuilt"
	"homelab/tests/harness"

	"homelab/details/repopath"
)

// The as-built record of the deployed estate is quiet and holds nothing real
// (#554).
//
// This is the question a fabricated state could not settle: whether the
// record comes out quiet against what is actually running. A record that is
// not quiet makes every plan against it show changes that are not real, and
// one that still holds a real value cannot be published at all. Either is a
// failure here, the night it starts.
//
// It calls the same details/asbuilt the Record phase does, in the workspace
// this tier already has, rather than running a second contractor that would
// render and sterilize the workspace out from under the tests after it.
//
// Then it is used the way a pull request uses it: saved, and the code as it
// stands planned against it, step by step as the converge would apply it. That plan has to be quiet too - a record that
// is quiet on its own terms but not once saved and read back would put false
// changes in every pull request's plan.
//
// Only names reach this log: resource types, attribute names and where each
// finding came from. Never a value, and never an instance key.
// covers: verb:record-as-built
// covers: verb:plan-as-built
func TestTheAsBuiltRecordIsQuietAndHoldsNothingReal(t *testing.T) {
	root := repopath.RootOrFail(t)
	committed, err := os.ReadFile(filepath.Join(root, "config", "management.tpl.json"))
	if err != nil {
		t.Fatal(err)
	}
	// The template as the contractor plans from it: the committed one with
	// the site's applications added, which the template itself never names.
	// Planned from the committed text alone, the site has no applications,
	// and everything made for one reads as about to be destroyed.
	tpl, err := config.Compose(committed, root, harness.Site())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := os.ReadFile(filepath.Join(root, "config", "management.rendered.json"))
	if err != nil {
		t.Fatalf("the rendered config is missing; this tier runs after a phase that renders it: %v", err)
	}
	work := filepath.Join(root, ".as-built")
	saved := filepath.Join(t.TempDir(), "saved")
	t.Cleanup(func() { _ = os.RemoveAll(work) })

	// Each of the site's roots, the cluster's first: the platform root's
	// offline plans run with the stand-ins the cluster's record holds for
	// the access it is configured from.
	var access map[string]string
	for _, name := range config.Roots {
		opts := harness.TofuOptions(t, name, nil)
		terraform.Init(t, opts)
		if name == config.ClusterRoot {
			harness.HandOverRecordedHardware(t, opts)
		}
		// The real plan Take makes needs what the options carry: for the
		// platform root, the cluster's real access, and for the cluster's
		// the hardware its state records.
		for k, v := range opts.EnvVars {
			t.Setenv(k, v)
		}

		res, err := asbuilt.Take(asbuilt.Inputs{
			Root: opts.TerraformDir, Work: work,
			Template: tpl, Rendered: rendered, Site: harness.Site(),
			MachineSecrets: name == config.ClusterRoot,
			Vars:           vars(name, access),
			Progress:       func(s string) { t.Log(name + ": " + s) },
		}, asbuilt.Exec)
		_ = os.RemoveAll(work)
		var pending *asbuilt.NotConvergedError
		if errors.As(err, &pending) {
			t.Fatalf("the %s root has pending changes, so no record can be taken; TestDeployedEstateMatchesTheCode says which", name)
		}
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("%s: replaced %v; quiet: %v after %d round(s)", name, res.Replaced, res.Quiet, res.Rounds)
		if !res.Quiet {
			pending, _ := asbuilt.Pending(res.LastPlan)
			t.Errorf("the %s root's record is not quiet after %d rounds, so every plan against it would show these:\n  %s",
				name, res.Rounds, strings.Join(pending, "\n  "))
		}
		for _, f := range res.Before {
			t.Errorf("%s: a real value survived the replacing: %s", name, f)
		}
		for _, k := range res.KeyedByAValue {
			t.Errorf("%s: %s is keyed by a real value, so every plan, apply and log that names it prints the value", name, k)
		}
		for _, f := range res.Computed() {
			t.Logf("%s: computed by the offline plan from the record and public code: %s", name, f)
		}
		if !res.Publishable() {
			return
		}
		if name == config.ClusterRoot {
			if access, err = asbuilt.OutputVars(res.State, steps.PlatformInputs...); err != nil {
				t.Fatal(err)
			}
		}

		record := filepath.Join(saved, name)
		if err := asbuilt.Write(record, res, asbuilt.Meta{Site: harness.Site()}); err != nil {
			t.Fatal(err)
		}
		plan, _, err := asbuilt.PlanAgainst(asbuilt.PlanInputs{
			Root: opts.TerraformDir, Work: work, Record: record,
			Template: tpl, Site: harness.Site(),
			PluginDir: filepath.Join(opts.TerraformDir, ".terraform", "providers"),
			Sequence:  steps.Plan(name),
			Vars:      vars(name, access),
		}, asbuilt.Exec)
		_ = os.RemoveAll(work)
		if err != nil {
			t.Fatal(err)
		}
		if pending, _ := asbuilt.Pending(plan); len(pending) > 0 {
			t.Errorf("the %s root as it stands, planned against its own saved record, would change:\n  %s",
				name, strings.Join(pending, "\n  "))
		}
	}
}

// vars is what a root's offline plan is given beyond the site and the config:
// the cluster's access for the platform root, nothing for the cluster's own.
func vars(root string, access map[string]string) map[string]string {
	if root == config.PlatformRoot {
		return access
	}
	return nil
}
