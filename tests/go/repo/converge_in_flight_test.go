package repo

import (
	"strings"
	"testing"

	"homelab/details/capacity"
)

// A converge in flight is not evicted (docs/epochs/04-observability.md,
// criterion 13), by any of the three ways a running pod is taken off its
// machine:
//
//   - a drain waits for it: a disruption budget allows none of the
//     converge's runners to be taken, in the namespace the runners are in;
//   - nothing below it can take its room, and what it takes room from is
//     only work that can wait: the class it runs at may put off lower work,
//     and every class below it is batch;
//   - under memory pressure it is not the pod over its reservation, which
//     is a size read from what it uses and is not held here.
//
// FOUND BY WHAT IT IS. The converge's runners are the set the estate's
// converge asks for; the budget is whichever one selects them; the classes
// are every PriorityClass the site declares.
func TestAConvergeInFlightIsNotEvicted(t *testing.T) {
	tree := repository(t)
	_, manifest := fluxObject(t, kindHelmRelease, runnerScaleSet)
	set := yamlScalar(t, manifest, "runnerScaleSetName")
	var namespace, class string
	budgets := 0
	classes := map[string]map[string]any{}
	for _, rel := range tree.Files(t) {
		if !authoredHere(t, rel) || !strings.HasPrefix(rel, "clusters/") || !strings.HasSuffix(rel, ".yaml") {
			continue
		}
		for _, doc := range mappings(t, tree, rel) {
			switch doc["kind"] {
			case kindHelmRelease:
				if walk(doc, "metadata", "name") == runnerScaleSet {
					namespace, _ = walk(doc, "metadata", "namespace").(string)
					class, _ = walk(doc, "spec", "values", "template", "spec", capacity.PriorityClassKey).(string)
				}
			case capacity.KindPriorityClass:
				name, _ := walk(doc, "metadata", "name").(string)
				classes[name] = doc
			case "PodDisruptionBudget":
				if walk(doc, "spec", "selector", "matchLabels", poolLabel) != set {
					continue
				}
				budgets++
				if walk(doc, "metadata", "namespace") != namespace && namespace != "" {
					t.Errorf("%s holds the converge's runners in %v, and they are in %s: a budget in another namespace holds nothing", rel, walk(doc, "metadata", "namespace"), namespace)
				}
				if n, ok := walk(doc, "spec", "maxUnavailable").(int); !ok || n != 0 {
					t.Errorf("%s lets %v of the converge's runners be taken at once. A drain then cuts a converge off half-way; it must allow none.", rel, walk(doc, "spec", "maxUnavailable"))
				}
			}
		}
	}
	if budgets == 0 {
		t.Errorf("no disruption budget selects the converge's runners (%s), so a drain takes a converge in flight off its machine half-way", set)
	}

	own, ok := classes[class]
	if !ok {
		t.Fatalf("the converge's runners run at %q, which is not a class the site declares", class)
	}
	if own[capacity.PreemptionKey] != capacity.PreemptsLower {
		t.Errorf("the converge's runners run at %q, which may not take room from lower work: on a site run full, a converge waits for batch work to finish", class)
	}
	value, _ := own["value"].(int)
	below := 0
	for name, c := range classes {
		if v, _ := c["value"].(int); v < value {
			below++
			if name != capacity.CanWait {
				t.Errorf("%q is below %q, so a converge may put it off to start; only work that can wait (%s) may be below it", name, class, capacity.CanWait)
			}
		}
	}
	if below == 0 {
		t.Errorf("nothing is below %q, so there is no work that can wait for a converge to take room from", class)
	}
}
