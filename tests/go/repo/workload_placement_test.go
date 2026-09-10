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
		// `affinity` and `priorityClassName` are top-level.
		Values:   nil,
		Priority: "critical",
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
	},
	{
		What:                  "a CI runner",
		File:                  "clusters/management/infrastructure/configs/runner-scale-set.yaml",
		Release:               "self-hosted",
		ChartVersion:          "0.14.2",
		Values:                []string{"template", "spec"},
		ResourcesInContainers: true,
		Priority:              "batch",
	},
	{
		What:         "the CloudNativePG operator",
		File:         "clusters/management/infrastructure/controllers/cloudnative-pg.yaml",
		Release:      "cloudnative-pg",
		ChartVersion: "0.23.0",
		// charts/cloudnative-pg/values.yaml, consumed by templates/deployment.yaml.
		Values:   nil,
		Priority: "critical",
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
	seen := 0
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
			seen++
			if !described[rel+"#"+name] {
				undescribed = append(undescribed, rel+" -> "+name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking clusters/: %v", err)
	}

	// This is the fail-closed half of the table above: a HelmRelease nobody
	// described is a failure rather than a skip. That property depends
	// entirely on the walk finding the releases, and finding none passes -
	// undescribed stays empty and the check reports success having enumerated
	// nothing.
	//
	// So the walk has to prove it worked. Moving clusters/, or nesting the
	// manifests under a directory this skips, would otherwise turn the guard
	// off while leaving it green.
	if seen == 0 {
		t.Fatal(`no HelmRelease was found anywhere under clusters/, so this proves nothing.

The whole point of this check is that a release nobody described is a failure
rather than an omission. With none found it cannot tell a fully-described
estate from a walk that is reading the wrong tree.`)
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
