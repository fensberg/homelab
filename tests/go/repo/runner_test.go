package repo

import (
	"homelab/contractor/config"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"slices"
	"sort"
)

// The runner manifests name their namespaces as literals, while OpenTofu
// declares the same values in modules/infrastructure/platform/variables.tf and creates
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
// The runners' two Helm releases, which is what their manifests are found by.
const (
	runnerController = "gha-runner-scale-set-controller"
	runnerScaleSet   = "self-hosted"
)

// runnerSets is the release of every runner scale set the repository
// declares, found by the chart it installs. The runners are more than one
// set, split by the priority of their work, and a rule about "the runners"
// that read one of them by name would hold for that one and say nothing of
// the next.
func runnerSets(t *testing.T) []string {
	t.Helper()
	var sets []string
	for _, r := range whatReserves(t) {
		kind, name, _ := strings.Cut(r.object, "/")
		if kind != kindHelmRelease {
			continue
		}
		if regexp.MustCompile(`(?m)^\s*chart:\s*gha-runner-scale-set\s*$`).MatchString(helmReleaseDocument(t, r.file, name)) {
			sets = append(sets, name)
		}
	}
	sort.Strings(sets)
	if !slices.Contains(sets, runnerScaleSet) {
		t.Fatalf("the runner scale sets found are %v, and the one every site's converge runs on (%s) is not among them", sets, runnerScaleSet)
	}
	return sets
}

// helmReleaseDocument is the one document of a manifest that declares the
// named release, so that two releases in one file are read apart.
func helmReleaseDocument(t *testing.T, rel, name string) string {
	t.Helper()
	for _, doc := range strings.Split(readRepoFile(t, rel), "\n---") {
		if regexp.MustCompile(`(?m)^kind:\s*HelmRelease\s*$`).MatchString(doc) &&
			regexp.MustCompile(`(?m)^  name:\s*`+regexp.QuoteMeta(name)+`\s*$`).MatchString(doc) {
			return doc
		}
	}
	t.Fatalf("%s does not declare the release %s", rel, name)
	return ""
}

func TestRunnerManifestsAgreeWithOpenTofu(t *testing.T) {
	tfPath, tf := tofuDeclaring(t, "runner_system_namespace =")

	for _, tc := range []struct {
		local   string
		release string
	}{
		{"runner_system_namespace", runnerController},
		{"runners_namespace", runnerScaleSet},
	} {
		want := hclStringLocal(t, tf, tc.local)
		path, manifest := fluxObject(t, kindHelmRelease, tc.release)
		if !strings.Contains(manifest, want) {
			t.Errorf("%s declares local.%s = %q, but %s never mentions it. The manifest and the OpenTofu have drifted; one of them is now describing a resource the other does not create.",
				tfPath, tc.local, want, path)
		}
	}
}

// runs-on is the contract between a workflow and the scale set, and with
// runner scale sets it matches the installation name exactly rather than being
// one label among several. A rename on either side silently orphans every
// workflow targeting it - the job queues forever rather than failing.
func TestEveryJobOnTheEstatesRunnersNamesItsSite(t *testing.T) {
	// Read from the manifest, which is where the name is declared. It used to
	// be read from an OpenTofu local that nothing in OpenTofu used - a value
	// kept alive only so this test could compare against it, which tflint
	// correctly called dead code. The manifest is the only declaration now,
	// so it is the one this test reads.
	// Read from the manifests, which is where the names are declared: one
	// for each set of runners, and every one of them carries the site.
	//
	// One manifest serves every site, so a name has to carry the site: a
	// fixed name would register two sites' runners as one pool, and a job
	// for one site would be handed to the other.
	var prefixes []string
	for _, release := range runnerSets(t) {
		_, manifest := fluxObject(t, kindHelmRelease, release)
		name := yamlScalar(t, manifest, "runnerScaleSetName")
		prefix, perSite := strings.CutSuffix(name, "${SITE}")
		if !perSite || prefix == "" {
			t.Fatalf("the runner scale set %s is named %q. Every site reconciles this manifest, so the name must end in ${SITE} - under one fixed name, two sites offer their runners for each other's work.", release, name)
		}
		prefixes = append(prefixes, prefix)
	}
	// Longest first, so a job asking for one set is not read as asking for
	// another whose name its name begins with.
	sort.Slice(prefixes, func(i, j int) bool { return len(prefixes[i]) > len(prefixes[j]) })
	prefix := prefixes[len(prefixes)-1]

	// Every workflow, not the two that used the runners when this was
	// written: a job anywhere that asks for one of the estate's runners has
	// to ask for a site's, by the matrix it runs for or by a site the config
	// declares. Anything else waits for a runner that will never appear.
	runsOn := regexp.MustCompile(`(?m)^\s*runs-on:\s*(\S.*?)\s*$`)
	asked := 0
	askedOf := map[string]int{}
	for _, wf := range tracked(t, func(rel string) bool {
		return strings.HasPrefix(rel, ".github/workflows/") && strings.HasSuffix(rel, ".yml") && strings.Count(rel, "/") == 2
	}) {
		for _, m := range runsOn.FindAllStringSubmatch(readRepoFile(t, wf), -1) {
			site, estate := "", false
			for _, p := range prefixes {
				if site, estate = strings.CutPrefix(m[1], p); estate {
					prefix = p
					break
				}
			}
			if !estate {
				continue
			}
			asked++
			askedOf[prefix]++
			// An expression, never a site written out: which sites there are
			// is the config's to say, and a name here is one the next site
			// is not.
			if !strings.HasPrefix(site, "${{") || !strings.HasSuffix(site, "}}") || !strings.Contains(site, "site") {
				t.Errorf("%s declares `runs-on: %s`. The estate's runners are registered per site, as %s<site>, so a job names the site it runs for with an expression: %s${{ matrix.site }} for a job that runs once per site.", wf, m[1], prefix, prefix)
			}
		}
	}
	if asked == 0 {
		t.Fatalf("no workflow asks for a runner named %s<site>, so either nothing runs on the estate or this has stopped reading the workflows", prefix)
	}
	// And every set is asked for by something. A set of runners no job asks
	// for is a listener, a registration with GitHub and a manifest to keep
	// up, all for nothing: the kind of thing that is added for a change that
	// never quite landed and is then there for good.
	for _, p := range prefixes {
		if askedOf[p] == 0 {
			t.Errorf("no workflow asks for a runner named %s<site>, so that set of runners serves nothing. Point the job it was made for at it, or take the set out.", p)
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
		t.Fatalf("no string local named %q beside the runner's other names", name)
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
	// Every set of runners, not the one this was written for.
	for _, release := range runnerSets(t) {
		t.Run(release, func(t *testing.T) { runnersKeepOffControlPlanes(t, release) })
	}
}

// runnersKeepOffControlPlanes is that rule for one set of runners, by its release.
func runnersKeepOffControlPlanes(t *testing.T, release string) {
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
	_, body := fluxObject(t, kindHelmRelease, release)
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("parsing the runner scale set: %v", err)
	}

	req := doc.Spec.Values.Template.Spec.Affinity.NodeAffinity.Required
	if req == nil {
		t.Fatal("the runner pod has no required node affinity.\n\nA preferred one is not enough: it puts CI back onto the control plane exactly when the workers are busy, which is when a build is heaviest and when etcd can least afford to share four cores with it.")
	}

	controlPlane := map[string]string{
		"kubernetes.io/os":       "linux",
		config.ControlPlaneLabel: "",
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
	// Every set of runners, not the one this was written for.
	for _, release := range runnerSets(t) {
		t.Run(release, func(t *testing.T) { runnersAreNotBestEffort(t, release) })
	}
}

// runnersAreNotBestEffort is that rule for one set of runners, by its release.
func runnersAreNotBestEffort(t *testing.T, release string) {
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
	_, body := fluxObject(t, kindHelmRelease, release)
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
