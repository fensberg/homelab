//go:build integration

package integration_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gruntwork-io/terratest/modules/terraform"

	"homelab/details/asbuilt"
	"homelab/tests/harness"
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
// Only names reach this log: resource types, attribute names and where each
// finding came from. Never a value, and never an instance key.
// covers: verb:record-as-built
func TestTheAsBuiltRecordIsQuietAndHoldsNothingReal(t *testing.T) {
	opts := harness.TofuOptions(t, nil)
	terraform.Init(t, opts)

	root := harness.RepoRoot(t)
	tpl, err := os.ReadFile(filepath.Join(root, "config", "management.tpl.json"))
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := os.ReadFile(filepath.Join(root, "config", "management.rendered.json"))
	if err != nil {
		t.Fatalf("the rendered config is missing; this tier runs after a phase that renders it: %v", err)
	}
	work := filepath.Join(root, ".as-built")
	t.Cleanup(func() { _ = os.RemoveAll(work) })

	res, err := asbuilt.Take(asbuilt.Inputs{
		Root: opts.TerraformDir, Work: work,
		Template: tpl, Rendered: rendered, Site: harness.Site(),
		Progress: func(s string) { t.Log(s) },
	}, tofu)
	var pending *asbuilt.NotConvergedError
	if errors.As(err, &pending) {
		t.Fatal("the estate has pending changes, so no record can be taken; TestDeployedEstateMatchesTheCode says which")
	}
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("replaced %v; quiet: %v after %d round(s)", res.Replaced, res.Quiet, res.Rounds)
	if !res.Quiet {
		pending, _ := asbuilt.Pending(res.LastPlan)
		t.Errorf("the record is not quiet after %d rounds, so every plan against it would show these:\n  %s",
			res.Rounds, strings.Join(pending, "\n  "))
	}
	for _, f := range res.Before {
		t.Errorf("a real value survived the replacing: %s", f)
	}
	for _, f := range res.Computed() {
		t.Logf("computed by the offline plan from the record and public code: %s", f)
	}
}

func tofu(dir string, env []string, args ...string) ([]byte, []byte, error) {
	c := exec.Command("tofu", args...)
	c.Dir = dir
	if env != nil {
		c.Env = env
	}
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	err := c.Run()
	return out.Bytes(), errb.Bytes(), err
}
