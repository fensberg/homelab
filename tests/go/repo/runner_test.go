package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The runner manifests name the namespaces and the credential secret as
// literals, while OpenTofu declares the same three values in
// management/cluster/variables.tf and creates those resources from them. Two declarations of one fact, which is only safe if something
// asserts they agree - the same reasoning that has registry.tf and config.go
// implementing the config contract twice.
//
// The alternative was substituting all of them through Flux, as the state
// database does. That was rejected for a boundary reason rather than a design
// one: every substituted variable has to be listed in pr-validation.yml's
// envsubst environment for kubeconform to see a complete manifest, and the
// agent that writes these files deliberately holds no `workflows` permission,
// so it cannot edit that file. Literals plus this test reach the same place
// without needing a permission the boundary is built to withhold.
func TestRunnerManifestsAgreeWithOpenTofu(t *testing.T) {
	tf := readRepoFile(t, "management/cluster/variables.tf")

	for _, tc := range []struct {
		local    string
		manifest string
		mustHave []string
	}{
		{"runner_system_namespace", "clusters/management/infrastructure/controllers/actions-runner-controller.yaml", nil},
		{"runners_namespace", "clusters/management/infrastructure/configs/runner-scale-set.yaml", nil},
		{"runner_secret_name", "clusters/management/infrastructure/configs/runner-scale-set.yaml", nil},
	} {
		want := hclStringLocal(t, tf, tc.local)
		manifest := readRepoFile(t, tc.manifest)
		if !strings.Contains(manifest, want) {
			t.Errorf("%s declares local.%s = %q, but %s never mentions it. The manifest and the OpenTofu have drifted; one of them is now describing a resource the other does not create.",
				"management/cluster/variables.tf", tc.local, want, tc.manifest)
		}
	}
}

// runs-on is the contract between a workflow and the scale set, and with
// runner scale sets it matches the installation name exactly rather than being
// one label among several. A rename on either side silently orphans every
// workflow targeting it - the job queues forever rather than failing.
func TestRunnerScaleSetNameMatchesRunsOn(t *testing.T) {
	// Read from the manifest, which is where the name is declared. It used to
	// be read from an OpenTofu local that nothing in OpenTofu used - a value
	// kept alive only so this test could compare against it, which tflint
	// correctly called dead code. The manifest is the only declaration now,
	// so it is the one this test reads.
	manifest := readRepoFile(t, "clusters/management/infrastructure/configs/runner-scale-set.yaml")
	name := yamlScalar(t, manifest, "runnerScaleSetName")

	for _, wf := range []string{
		".github/workflows/deploy-infrastructure.yml",
		".github/workflows/integration-tests.yml",
	} {
		body := readRepoFile(t, wf)
		if !strings.Contains(body, "runs-on: "+name) {
			t.Errorf("%s does not declare `runs-on: %s`. The scale set is registered under that name, so a job asking for anything else waits for a runner that will never appear.", wf, name)
		}
	}
}

// yamlScalar pulls one `key: value` scalar out of a manifest. Deliberately not
// a YAML parse: the file carries a ${VAR} Flux substitutes at reconcile time,
// which is not a thing a strict decode has to cope with just to read one field.
func yamlScalar(t *testing.T, body, key string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `:\s*(\S+)\s*$`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no %s: scalar in the scale set manifest", key)
	}
	return m[1]
}

func hclStringLocal(t *testing.T, body, name string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `\s*=\s*"([^"]+)"`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no string local named %q in management/cluster/variables.tf", name)
	}
	return m[1]
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(b)
}
