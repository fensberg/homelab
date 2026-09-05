package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The runner manifests name their namespaces as literals, while OpenTofu
// declares the same values in management/cluster/variables.tf and creates
// those namespaces from them.
//
// The credential secret's name is deliberately absent from this check: it is
// substituted by Flux from the same local, so the two cannot drift by
// construction. Substitution beats an assertion wherever it is available -
// this test only exists for the values that could not be substituted, because
// a ${VAR} in metadata.name does not survive CI rendering it empty. Two declarations of one fact, which is only safe if something
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

// --- CI stays off the machines holding quorum -------------------------------

// The affinity is evaluated rather than matched.
//
// A test asserting that the manifest contains the string "DoesNotExist" passes
// just as happily when the key is wrong, when the operator has been flipped to
// Exists, or when the whole term has been moved into the `preferred` block -
// where it excludes nothing at all. So this implements the small part of the
// scheduler's own rule that the property depends on, and asks it the two
// questions that matter: would this pod land on a control plane, and can it
// land anywhere.
//
// Terms are OR'd; the expressions inside one term are AND'd. That is the
// scheduler's semantics, and getting it backwards is exactly the mistake this
// is here to catch.
func nodeAffinityAdmits(t *testing.T, terms []any, labels map[string]string) bool {
	t.Helper()
	for _, term := range terms {
		m, ok := term.(map[string]any)
		if !ok {
			t.Fatalf("a nodeSelectorTerm is not a mapping: %#v", term)
		}
		exprs, ok := m["matchExpressions"].([]any)
		if !ok {
			t.Fatalf("a nodeSelectorTerm has no matchExpressions: %#v", m)
		}
		all := true
		for _, e := range exprs {
			em := e.(map[string]any)
			key, _ := em["key"].(string)
			op, _ := em["operator"].(string)
			_, present := labels[key]
			switch op {
			case "Exists":
				all = all && present
			case "DoesNotExist":
				all = all && !present
			default:
				// Refused rather than ignored. An operator this evaluator does
				// not model would otherwise be silently treated as a pass,
				// which is the failure mode the whole test exists to avoid.
				t.Fatalf("this test does not model the %q operator; teach it before using one", op)
			}
		}
		if all {
			return true
		}
	}
	return false
}

// CI must never be schedulable onto a control plane, and must be schedulable
// onto a worker.
//
// The first half is the safety property: eight concurrent jobs pulling large
// images alongside etcd is how a control plane becomes intermittently unwell,
// and one integration run was already killed for competing with it (#236).
// The second half is the guard against overcorrecting - an affinity so narrow
// that nothing schedules would satisfy the first assertion perfectly while
// taking CI off the estate entirely.
func TestRunnerPodsCannotScheduleOntoAControlPlane(t *testing.T) {
	var doc struct {
		Spec struct {
			Values struct {
				Template struct {
					Spec struct {
						Affinity struct {
							NodeAffinity struct {
								Required *struct {
									NodeSelectorTerms []any `yaml:"nodeSelectorTerms"`
								} `yaml:"requiredDuringSchedulingIgnoredDuringExecution"`
							} `yaml:"nodeAffinity"`
						} `yaml:"affinity"`
					} `yaml:"spec"`
				} `yaml:"template"`
			} `yaml:"values"`
		} `yaml:"spec"`
	}
	body := readRepoFile(t, "clusters/management/infrastructure/configs/runner-scale-set.yaml")
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("parsing the runner scale set: %v", err)
	}

	req := doc.Spec.Values.Template.Spec.Affinity.NodeAffinity.Required
	if req == nil {
		t.Fatal("the runner pod has no required node affinity.\n\nA preferred one is not enough: it puts CI back onto the control plane exactly when the workers are busy, which is when a build is heaviest and when etcd can least afford to share four cores with it.")
	}

	controlPlane := map[string]string{
		"kubernetes.io/os":                      "linux",
		"node-role.kubernetes.io/control-plane": "",
	}
	worker := map[string]string{"kubernetes.io/os": "linux"}

	if nodeAffinityAdmits(t, req.NodeSelectorTerms, controlPlane) {
		t.Error("a CI runner would schedule onto a control-plane node, which is the thing the worker pool was built to stop.")
	}
	if !nodeAffinityAdmits(t, req.NodeSelectorTerms, worker) {
		t.Error("a CI runner would not schedule onto a worker either, so this affinity takes CI off the estate rather than moving it.")
	}
}

// A runner with no requests is BestEffort, which is the first cgroup the OOM
// controller reaches for - it is what killed the integration run in #234. The
// requests are also what the scheduler reserves, so a build lands on a node
// that can hold it rather than finding out at minute twelve.
//
// Deliberately no assertion that a limit is absent. That is a judgement this
// test should not freeze: #234 argues against a memory limit because capping a
// build turns an infrastructure shortfall into a red test, but that reasoning
// belongs in the record and in review, not in a check that would fail the day
// somebody has a good reason.
func TestRunnerPodsAreNotBestEffort(t *testing.T) {
	var doc struct {
		Spec struct {
			Values struct {
				Template struct {
					Spec struct {
						Containers []struct {
							Name      string `yaml:"name"`
							Resources struct {
								Requests map[string]string `yaml:"requests"`
							} `yaml:"resources"`
						} `yaml:"containers"`
					} `yaml:"spec"`
				} `yaml:"template"`
			} `yaml:"values"`
		} `yaml:"spec"`
	}
	body := readRepoFile(t, "clusters/management/infrastructure/configs/runner-scale-set.yaml")
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("parsing the runner scale set: %v", err)
	}

	containers := doc.Spec.Values.Template.Spec.Containers
	if len(containers) == 0 {
		t.Fatal("the runner scale set declares no containers, so this check is asserting nothing")
	}
	for _, c := range containers {
		for _, want := range []string{"cpu", "memory"} {
			if c.Resources.Requests[want] == "" {
				t.Errorf("container %q requests no %s, so it is BestEffort and the OOM controller will choose it first (#234)", c.Name, want)
			}
		}
	}
}
