package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Every pod this estate deploys is sized, placed and given a priority.
//
// WHAT THIS IS GUARDING AGAINST, which is not the obvious thing.
//
// The obvious failure is a workload with no resource requests: BestEffort, and
// therefore the OOM controller's first choice on a node under pressure. Five
// of them were found that way (#237), including both pods that manage CI and
// the operator that manages the state database - so the failure ate its own
// supervisor.
//
// The failure that is harder to see, and the reason this is a table rather
// than four separate tests, is that **a Helm value at a path the chart does
// not read is accepted in silence**. There is no error, no warning and no
// event; the manifest says the pod has requests and the pod does not. Every
// path below was read out of the pinned chart's own values.yaml and confirmed
// against the template that consumes it, and `chartVersion` records which
// version that was. Bump a chart and this test goes red until somebody has
// re-read the values file - which turns "remember to check the paths" into a
// step the build takes rather than one a person takes.
//
// It is fail-closed in the direction that matters: a HelmRelease that is not
// in the table is a failure, not a skip. "Nobody told me where this chart's
// pod spec lives" and "this chart is fine" must not produce the same exit
// code.

// The canonical way this estate says "not on a machine holding quorum".
//
// Expressed as the absence of the control-plane role rather than the presence
// of a worker label, because Kubernetes sets the control-plane label itself
// and a label we invented would be one more thing that can go missing - and a
// selector on a missing label fails closed in the wrong direction, silently
// unschedulable rather than obviously wrong.
//
// These two synthetic nodes are what the affinity is evaluated against. A
// string comparison against the expected YAML would pass for a rule that had
// been rewritten into something equivalent-looking and wrong; running the
// selector cannot.
var (
	aControlPlaneNode = map[string]string{
		"kubernetes.io/os":                      "linux",
		"node-role.kubernetes.io/control-plane": "",
	}
	aWorkerNode = map[string]string{"kubernetes.io/os": "linux"}
)

// workloadPod is one pod-producing thing, and where in its manifest the fields
// that decide sizing, placement and priority actually live.
type workloadPod struct {
	// What it is, in the words a failure message should use.
	What string
	// Repository-relative manifest.
	File string
	// metadata.name of the HelmRelease in that file.
	Release string
	// The chart version whose values.yaml the paths below were read from. Not
	// decoration: if the manifest has moved on from this, nobody has checked
	// that the paths still exist.
	ChartVersion string
	// Path from spec.values to the mapping that behaves like a PodSpec -
	// where affinity and priorityClassName are read from. Empty means the
	// values root, which is what a chart with top-level `affinity` uses.
	Values []string
	// Where the requests are. Most charts take a `resources` key beside the
	// placement fields; pod-template-shaped values put them on each entry of
	// a `containers` list instead.
	ResourcesInContainers bool
	// The priority class it belongs in, per the table in
	// docs/epochs/02-abstraction.md.
	Priority string

	// Where this workload's security properties are declared. Charts disagree
	// about both the path and the key names - one chart's container-level key
	// is `securityContext`, another's is `containerSecurityContext`, and a
	// third has none that any template reads - so neither is guessable and
	// both were read out of the pinned chart.
	//
	// SecurityValues is the path from spec.values to the mapping holding
	// PodSecurityKey; nil is the values root. When ResourcesInContainers, the
	// container-level context sits on each container beside its resources, the
	// way a PodSpec has it, and ContainerSecurityKey names the key there.
	SecurityValues       []string
	PodSecurityKey       string
	ContainerSecurityKey string
	// Unasserted is why a level is not asserted, and must be non-empty
	// whenever either key above is empty. An omission and a considered
	// exemption are the same silence from here, so the exemption has to say
	// something.
	Unasserted string
}

// Read from the pinned charts on 2026-09-06. Each entry names the file that
// was read, because the next person to touch this should re-read the same one.
var workloadPods = []workloadPod{
	{
		What:         "the Actions Runner Controller",
		File:         "clusters/management/infrastructure/controllers/actions-runner-controller.yaml",
		Release:      "gha-runner-scale-set-controller",
		ChartVersion: "0.14.2",
		// charts/gha-runner-scale-set-controller/values.yaml: `resources`,
		// `affinity` and `priorityClassName` are top-level, and so are both
		// security keys - `podSecurityContext` on the pod and `securityContext`
		// on the container, each wrapped in `{{- with }}` in
		// templates/deployment.yaml so an empty map renders nothing.
		Values:               nil,
		Priority:             "critical",
		PodSecurityKey:       "podSecurityContext",
		ContainerSecurityKey: "securityContext",
	},
	{
		What:         "the runner listener",
		File:         "clusters/management/infrastructure/configs/runner-scale-set.yaml",
		Release:      "self-hosted",
		ChartVersion: "0.14.2",
		// listenerTemplate is a PodSpec copied verbatim into the
		// AutoscalingRunnerSet and merged by the controller, so resources sit
		// on the container rather than beside the placement fields.
		Values:                []string{"listenerTemplate", "spec"},
		ResourcesInContainers: true,
		Priority:              "critical",
		PodSecurityKey:        "securityContext",
		ContainerSecurityKey:  "securityContext",
	},
	{
		What:                  "a CI runner",
		File:                  "clusters/management/infrastructure/configs/runner-scale-set.yaml",
		Release:               "self-hosted",
		ChartVersion:          "0.14.2",
		Values:                []string{"template", "spec"},
		ResourcesInContainers: true,
		Priority:              "batch",
		PodSecurityKey:        "securityContext",
		ContainerSecurityKey:  "securityContext",
	},
	{
		What:         "the CloudNativePG operator",
		File:         "clusters/management/infrastructure/controllers/cloudnative-pg.yaml",
		Release:      "cloudnative-pg",
		ChartVersion: "0.23.0",
		// charts/cloudnative-pg/values.yaml, consumed by templates/deployment.yaml.
		// Note the container key is `containerSecurityContext` here and plain
		// `securityContext` on the chart above - the same property, two names,
		// which is the whole reason this is a table and not a convention.
		Values:               nil,
		Priority:             "critical",
		PodSecurityKey:       "podSecurityContext",
		ContainerSecurityKey: "containerSecurityContext",
	},
	{
		What:         "the OpenEBS Local PV provisioner",
		File:         "clusters/management/infrastructure/controllers/openebs.yaml",
		Release:      "openebs",
		ChartVersion: "4.6.0",
		// A subchart, so the keys are nested twice rather than top-level -
		// the exact shape a value silently lands at the wrong path in.
		Values:   []string{"localpv-provisioner", "localpv"},
		Priority: "critical",
		Unasserted: "the image declares no USER and runs as uid 0, so runAsNonRoot " +
			"would stop the provisioner starting rather than harden it; and the " +
			"subchart's `localpv.securityContext` is read by no template in it, " +
			"so the only container-level path available does nothing (#315)",
	},
}

// helmRelease is the part of a HelmRelease this file has an opinion about.
type helmRelease struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Chart struct {
			Spec struct {
				Version string `yaml:"version"`
			} `yaml:"spec"`
		} `yaml:"chart"`
		Values map[string]any `yaml:"values"`
	} `yaml:"spec"`
}

func readHelmReleases(t *testing.T, rel string) map[string]helmRelease {
	t.Helper()
	out := map[string]helmRelease{}
	dec := yaml.NewDecoder(strings.NewReader(readRepoFile(t, rel)))
	for {
		var doc helmRelease
		if err := dec.Decode(&doc); err != nil {
			break
		}
		if doc.Kind == "HelmRelease" {
			out[doc.Metadata.Name] = doc
		}
	}
	return out
}

// descend walks a path of mapping keys, reporting where it stopped rather than
// returning a bare nil. "the value is missing" and "the value is at a
// different path than the table claims" need different fixes.
func descend(root map[string]any, path []string) (map[string]any, error) {
	cur := root
	for i, key := range path {
		next, ok := cur[key]
		if !ok {
			return nil, fmt.Errorf("no %q under spec.values%s", key, pathString(path[:i]))
		}
		m, ok := next.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("spec.values%s is not a mapping", pathString(path[:i+1]))
		}
		cur = m
	}
	return cur, nil
}

func pathString(path []string) string {
	if len(path) == 0 {
		return ""
	}
	return "." + strings.Join(path, ".")
}

func requestsAreComplete(t *testing.T, resources any) error {
	t.Helper()
	m, ok := resources.(map[string]any)
	if !ok {
		return fmt.Errorf("resources is not a mapping")
	}
	requests, ok := m["requests"].(map[string]any)
	if !ok {
		return fmt.Errorf("resources declares no requests")
	}
	var missing []string
	for _, want := range []string{"cpu", "memory"} {
		if v, ok := requests[want]; !ok || v == nil || v == "" {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("requests no %s", strings.Join(missing, " and no "))
	}
	return nil
}

func TestEveryWorkloadIsSizedPlacedAndGivenAPriority(t *testing.T) {
	declared := declaredPriorityClasses(t)

	for _, w := range workloadPods {
		t.Run(w.What, func(t *testing.T) {
			releases := readHelmReleases(t, w.File)
			hr, ok := releases[w.Release]
			if !ok {
				t.Fatalf("%s declares no HelmRelease named %q, so this entry is asserting nothing", w.File, w.Release)
			}

			if got := hr.Spec.Chart.Spec.Version; got != w.ChartVersion {
				t.Fatalf(`%s is now on chart %s, but %s was written against %s.

The paths in this table are Helm values, and a value at a path the chart does
not read is accepted in silence - no error, no event, and a pod that looks
configured and is not. So a version bump is exactly when they need re-checking.

Read the new chart's values.yaml and the template that consumes each key,
correct the paths if they moved, and set ChartVersion to %s.`,
					w.File, got, w.What, w.ChartVersion, got)
			}

			podSpec, err := descend(hr.Spec.Values, w.Values)
			if err != nil {
				t.Fatalf("%s: %v.\n\nThe table says the pod spec for %s lives at spec.values%s in %s. Either it moved, or it was never written there - and if it was never written there, the cluster has been running without it and nothing said so.",
					w.File, err, w.What, pathString(w.Values), w.File)
			}

			// Priority.
			priority, _ := podSpec["priorityClassName"].(string)
			switch {
			case priority == "":
				t.Errorf(`%s has no priority class.

An unclassified pod is priority zero, below every class this estate declares -
so it is not merely last in the queue, it is below the work that is allowed to
be last. Set priorityClassName to %q, per the table in
docs/epochs/02-abstraction.md.`, w.What, w.Priority)
			case priority != w.Priority:
				t.Errorf("%s is %q, but this table says %q. One of the two is a decision nobody recorded.", w.What, priority, w.Priority)
			case !declared[priority]:
				t.Errorf(`%s names the priority class %q, which no manifest in this repository declares.

A pod naming a PriorityClass that does not exist is refused admission - it does
not fall back to zero. So this typo would take %s off the cluster entirely.`,
					w.What, priority, w.What)
			}

			// Placement.
			terms := requiredNodeSelectorTerms(t, podSpec)
			if terms == nil {
				t.Errorf(`%s has no required node affinity.

A preferred one is not enough. It puts the pod back onto a control plane
exactly when the workers are busiest, which is exactly when etcd can least
afford the company.`, w.What)
			} else {
				if nodeAffinityAdmits(t, terms, aControlPlaneNode) {
					t.Errorf("%s would schedule onto a control-plane node, which is what this whole arrangement exists to stop.", w.What)
				}
				if !nodeAffinityAdmits(t, terms, aWorkerNode) {
					t.Errorf("%s would not schedule onto a worker either, so this affinity takes it off the estate rather than moving it.", w.What)
				}
			}

			// Sizing.
			if w.ResourcesInContainers {
				containers, ok := podSpec["containers"].([]any)
				if !ok || len(containers) == 0 {
					t.Fatalf("%s declares no containers at spec.values%s.containers, so there is nothing to size", w.What, pathString(w.Values))
				}
				for _, c := range containers {
					cm, ok := c.(map[string]any)
					if !ok {
						t.Fatalf("a container under %s is not a mapping", w.What)
					}
					name, _ := cm["name"].(string)
					if err := requestsAreComplete(t, cm["resources"]); err != nil {
						t.Errorf("%s: container %q %s, so the pod is BestEffort and the OOM controller will choose it first (#237)", w.What, name, err)
					}
				}
				return
			}
			if err := requestsAreComplete(t, podSpec["resources"]); err != nil {
				t.Errorf("%s %s, so it is BestEffort and the OOM controller will choose it first (#237)", w.What, err)
			}
		})
	}
}

// requiredNodeSelectorTerms digs the hard node affinity out of a pod-spec-ish
// mapping, or returns nil if there is not one.
func requiredNodeSelectorTerms(t *testing.T, podSpec map[string]any) []any {
	t.Helper()
	affinity, ok := podSpec["affinity"].(map[string]any)
	if !ok {
		return nil
	}
	nodeAffinity, ok := affinity["nodeAffinity"].(map[string]any)
	if !ok {
		return nil
	}
	required, ok := nodeAffinity["requiredDuringSchedulingIgnoredDuringExecution"].(map[string]any)
	if !ok {
		return nil
	}
	terms, _ := required["nodeSelectorTerms"].([]any)
	return terms
}

// declaredPriorityClasses is every priority class this repository creates.
func declaredPriorityClasses(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	dec := yaml.NewDecoder(strings.NewReader(readRepoFile(t,
		"clusters/management/infrastructure/controllers/priority-classes.yaml")))
	for {
		var doc struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
		}
		if err := dec.Decode(&doc); err != nil {
			break
		}
		if doc.Kind == "PriorityClass" {
			out[doc.Metadata.Name] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("no PriorityClass is declared, so every priorityClassName in this repository points at nothing")
	}
	return out
}

// The half that stops the table above from being a list of the workloads
// somebody happened to think about.
//
// Without this, adding a chart and forgetting to add it here reads exactly
// like adding a chart that is fine. That is the shape of every guard this
// repository has had to repair: silence and success were indistinguishable.
func TestEveryHelmReleaseSaysWhereItsPodSpecLives(t *testing.T) {
	root := repoRoot(t)
	described := map[string]bool{}
	for _, w := range workloadPods {
		described[w.File+"#"+w.Release] = true
	}

	var undescribed []string
	clusters := filepath.Join(root, "clusters")
	err := filepath.WalkDir(clusters, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// flux-system holds Flux's own generated install, which is not a
			// HelmRelease at all and is checked separately below.
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		for name := range readHelmReleases(t, rel) {
			if !described[rel+"#"+name] {
				undescribed = append(undescribed, rel+" -> "+name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking clusters/: %v", err)
	}

	sort.Strings(undescribed)
	if len(undescribed) > 0 {
		t.Errorf(`%d HelmRelease(s) are not described in workloadPods:

  %s

Add an entry naming the chart version whose values.yaml you read and the path
its pod spec lives at. If the release genuinely produces no pod, say that in
an entry rather than leaving it out - an omission and a considered exemption
are the same silence from here.`, len(undescribed), strings.Join(undescribed, "\n  "))
	}
}

// --- Flux's own controllers -------------------------------------------------

// Flux is not a HelmRelease and never was BestEffort, so it is checked apart
// from the table above. Its generated manifest already carries requests and
// limits; what it does not carry is placement, and three of its four
// controllers - not four - carry a priority class.
//
// WHAT THIS PROVES AND WHAT IT DOES NOT. It reads the four Deployments out of
// gotk-components.yaml and applies the overlay's patches to them the way
// kustomize would: a patch whose target names no `name` applies to every
// Deployment, one that names a Deployment applies to that one. It does not
// shell out to kustomize, because this tier is hermetic - `task validate`
// builds the overlay for real and is where a malformed patch is caught.
//
// What it therefore catches is the failure that build cannot: a patch that
// builds perfectly and covers three controllers, or none, because a Flux
// upgrade renamed one or added a fifth.
func TestEveryFluxControllerIsPlacedAndGivenAPriority(t *testing.T) {
	declared := declaredPriorityClasses(t)
	const overlay = "clusters/management/flux-system/kustomization.yaml"

	// The generated install, as bootstrap wrote it.
	type deployment struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
		Spec struct {
			Template struct {
				Spec map[string]any `yaml:"spec"`
			} `yaml:"template"`
		} `yaml:"spec"`
	}
	deployments := map[string]map[string]any{}
	dec := yaml.NewDecoder(strings.NewReader(readRepoFile(t,
		"clusters/management/flux-system/gotk-components.yaml")))
	for {
		var doc deployment
		if err := dec.Decode(&doc); err != nil {
			break
		}
		if doc.Kind == "Deployment" {
			deployments[doc.Metadata.Name] = doc.Spec.Template.Spec
		}
	}
	if len(deployments) == 0 {
		t.Fatal("gotk-components.yaml declares no Deployment, so this check is asserting nothing about Flux")
	}

	// The overlay's patches, applied as kustomize would apply them.
	var k struct {
		Patches []struct {
			Target struct {
				Kind string `yaml:"kind"`
				Name string `yaml:"name"`
			} `yaml:"target"`
			Patch string `yaml:"patch"`
		} `yaml:"patches"`
	}
	if err := yaml.Unmarshal([]byte(readRepoFile(t, overlay)), &k); err != nil {
		t.Fatalf("parsing %s: %v", overlay, err)
	}
	for _, p := range k.Patches {
		if p.Target.Kind != "Deployment" {
			continue
		}
		var body struct {
			Spec struct {
				Template struct {
					Spec map[string]any `yaml:"spec"`
				} `yaml:"template"`
			} `yaml:"spec"`
		}
		if err := yaml.Unmarshal([]byte(p.Patch), &body); err != nil {
			t.Fatalf("parsing a patch in %s: %v", overlay, err)
		}
		for name, spec := range deployments {
			if p.Target.Name != "" && p.Target.Name != name {
				continue
			}
			for key, value := range body.Spec.Template.Spec {
				spec[key] = value
			}
		}
	}

	for _, name := range sortedKeys(deployments) {
		spec := deployments[name]

		priority, _ := spec["priorityClassName"].(string)
		switch {
		case priority == "":
			t.Errorf(`the Flux controller %q ends up with no priority class.

Unclassified is priority zero, below every class declared here. Flux's own
manifest gives three of its controllers system-cluster-critical and leaves
notification-controller unclassified; %s is not covered by either that or a
patch in %s.`, name, name, overlay)
		case strings.HasPrefix(priority, "system-"):
			// Upstream's own class on upstream's own components. Left alone
			// deliberately - see the overlay's comment.
		case !declared[priority]:
			t.Errorf("the Flux controller %q names the priority class %q, which no manifest here declares - so the pod would be refused admission rather than defaulted.", name, priority)
		}

		terms := requiredNodeSelectorTerms(t, spec)
		if terms == nil {
			t.Errorf(`the Flux controller %q has no required node affinity after the overlay is applied.

The patch in %s is what puts Flux on the workers. A Flux upgrade that renames
a controller, or adds one, slips out from under a patch without changing
anything that fails a build.`, name, overlay)
			continue
		}
		if nodeAffinityAdmits(t, terms, aControlPlaneNode) {
			t.Errorf("the Flux controller %q would schedule onto a control-plane node.", name)
		}
		if !nodeAffinityAdmits(t, terms, aWorkerNode) {
			t.Errorf("the Flux controller %q would not schedule onto a worker either, which would leave the estate with nothing reconciling it.", name)
		}
	}
}

func sortedKeys(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- asserted, not inherited ------------------------------------------------

// Every workload requires its security properties rather than inheriting them.
//
// WHAT THIS CATCHES, which is not "a pod running as root". Nothing in this
// estate runs as root today except the OpenEBS provisioner, and that is
// recorded rather than fixed. What it catches is the property arriving from
// somewhere this repository cannot see: the ARC controller does not run as
// root because its base image says `USER 65532`, and an upstream base-image
// change takes that away with no diff here, no event, and a pod that keeps
// starting. Asserted in the manifest, the same change fails admission - which
// is the estate's standing preference for loud over silently degraded.
//
// It is deliberately not a Semgrep rule or a lint. Those read the file; this
// reads the file against a table that records which chart version each path
// was checked in, so a chart bump re-opens the question instead of carrying
// the old answer forward.
func TestEveryWorkloadRequiresItsSecurityPropertiesRatherThanInheritingThem(t *testing.T) {
	for _, w := range workloadPods {
		t.Run(w.What, func(t *testing.T) {
			if w.PodSecurityKey == "" && w.ContainerSecurityKey == "" {
				if strings.TrimSpace(w.Unasserted) == "" {
					t.Fatalf(`%s asserts no security properties and says why nowhere.

Set PodSecurityKey and ContainerSecurityKey to the paths the pinned chart
actually reads, or set Unasserted to what reading it found. Leaving all three
empty is indistinguishable from nobody having looked.`, w.What)
				}
				return
			}
			if w.Unasserted != "" {
				t.Errorf("%s both asserts security properties and explains why it does not; one of the two is stale", w.What)
			}

			releases := readHelmReleases(t, w.File)
			hr, ok := releases[w.Release]
			if !ok {
				t.Fatalf("%s declares no HelmRelease named %q", w.File, w.Release)
			}
			at := w.SecurityValues
			if at == nil {
				at = w.Values
			}
			root, err := descend(hr.Spec.Values, at)
			if err != nil {
				t.Fatalf("%s: %v", w.File, err)
			}

			if w.PodSecurityKey != "" {
				pod, ok := root[w.PodSecurityKey].(map[string]any)
				if !ok {
					t.Errorf(`%s declares no %s at spec.values%s.

The pinned chart reads that path. Left unset it renders nothing, and whether
the pod runs as root becomes a property of whatever image the chart happens to
pull - which this repository cannot see change.`,
						w.What, w.PodSecurityKey, pathString(at))
				} else if pod["runAsNonRoot"] != true {
					t.Errorf("%s does not require runAsNonRoot: true; it has %v", w.What, pod["runAsNonRoot"])
				}
			}

			if w.ContainerSecurityKey == "" {
				return
			}
			for _, c := range containerSecurityContexts(t, w, root) {
				if c == nil {
					t.Errorf(`%s declares no %s on a container at spec.values%s.

Container-level is the level that matters for privilege escalation: a pod-level
runAsNonRoot says who the process is, and says nothing about what it may become.`,
						w.What, w.ContainerSecurityKey, pathString(at))
					continue
				}
				if c["allowPrivilegeEscalation"] != false {
					t.Errorf("%s does not refuse privilege escalation; it has %v", w.What, c["allowPrivilegeEscalation"])
				}
				if !dropsAllCapabilities(c) {
					t.Errorf("%s does not drop ALL capabilities, so it keeps whatever the runtime's default set happens to be", w.What)
				}
			}
		})
	}
}

// containerSecurityContexts returns one entry per container that must carry a
// context - nil where it is absent - so a missing one is reported rather than
// skipped by an empty range.
func containerSecurityContexts(t *testing.T, w workloadPod, root map[string]any) []map[string]any {
	t.Helper()
	if !w.ResourcesInContainers {
		c, _ := root[w.ContainerSecurityKey].(map[string]any)
		return []map[string]any{c}
	}
	containers, ok := root["containers"].([]any)
	if !ok || len(containers) == 0 {
		t.Fatalf("%s declares no containers, so there is nothing to secure", w.What)
	}
	out := make([]map[string]any, 0, len(containers))
	for _, raw := range containers {
		m, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("%s has a container that is not a mapping", w.What)
		}
		c, _ := m[w.ContainerSecurityKey].(map[string]any)
		out = append(out, c)
	}
	return out
}

// dropsAllCapabilities accepts the two spellings a manifest actually uses -
// a block sequence and an inline list - and nothing else. "ALL" is compared
// exactly, because Kubernetes does not accept "all".
func dropsAllCapabilities(sc map[string]any) bool {
	caps, ok := sc["capabilities"].(map[string]any)
	if !ok {
		return false
	}
	drop, ok := caps["drop"].([]any)
	if !ok {
		return false
	}
	for _, c := range drop {
		if s, ok := c.(string); ok && s == "ALL" {
			return true
		}
	}
	return false
}
