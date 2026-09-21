package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Terratest's default logger prints the output of every command it runs. The
// estate's OpenTofu root is the estate, so its outputs are estate values -
// kubeconfigs, connection strings, addresses - and the integration tier runs in
// a public repository's Actions log.
//
// WHAT THIS GUARDS. On 2026-09-21 the tier published a cluster-admin kubeconfig
// - CA, client certificate and `client-key-data` - once per test that touched
// the cluster, and the state database's connection string with its password
// alongside it (#491). A Kubernetes client certificate cannot be revoked: there
// is no CRL and no OCSP, so the API server trusts it until it expires, and the
// only true revocation is rotating the cluster CA.
//
// Nobody chose to print them. `harness.TofuOptions` simply did not set a
// Logger, and the default prints. That is the shape worth guarding: the
// dangerous behaviour was the one nobody wrote down.
//
// This is the second time the same rule has been learned here. `run.TofuApply`
// switched the contractor to a `-json` summary after a converge published a
// site name from a resource description into a public log, and the reasoning
// was written up carefully - in the contractor, which is not where the tests
// build their options. Fixing one place a rule is enforced is not fixing the
// rule.
//
// Two properties, because either alone is escapable: the options must be built
// in one place, and that place must silence the logger.
func TestTerratestNeverPrintsEstateOutputs(t *testing.T) {
	root := repoRoot(t)
	harnessDir := filepath.Join("tests", "go", "harness")

	// Assembled rather than written, so this file does not match itself.
	// Excluding it by name would work too and would be worse: an exemption
	// list is the thing a later file gets added to, and this guard is only
	// worth having while it reads every file under tests/go.
	needle := "terraform.Options" + "{"

	// One place builds them. A test constructing its own terraform.Options
	// gets terratest's default logger back, and the silence below would be
	// true and irrelevant.
	var offenders []string
	walked := 0
	err := filepath.Walk(filepath.Join(root, "tests", "go"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		walked++
		if strings.HasPrefix(rel, harnessDir+string(filepath.Separator)) {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(body), needle) {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking tests/go: %v", err)
	}
	if walked == 0 {
		t.Fatal("walked no Go files under tests/go, so this test proves nothing")
	}

	for _, rel := range offenders {
		t.Errorf(`%s builds its own terraform.Options.

They are built in tests/go/harness so that one place decides the logger.
Terratest's default prints every command's output, and the outputs of the
estate root are estate values - that is how a cluster-admin kubeconfig and the
state database password reached a public Actions log (#491).

Use harness.TofuOptions.`, rel)
	}

	// And that one place silences it.
	tofu := readFile(t, filepath.Join(root, harnessDir, "tofu.go"))
	if !regexp.MustCompile(`(?m)^\s*Logger:\s*logger\.Discard,`).MatchString(tofu) {
		t.Errorf(`tests/go/harness/tofu.go does not set Logger: logger.Discard on the options it builds.

Terratest's default logger prints the output of every command. This root's
outputs include the cluster kubeconfig and the state database connection
string, and this tier runs in a public repository's Actions log (#491).

A failing command still fails the test and still returns its error; what is
withheld is the value, and the detail is available by re-running on a
workstation.`)
	}
}
