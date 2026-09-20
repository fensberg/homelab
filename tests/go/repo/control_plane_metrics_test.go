package repo

import (
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Scraping a control-plane component takes two changes, in two tiers, and
// either alone is wrong.
//
// Talos binds the scheduler and the controller-manager to 127.0.0.1 and leaves
// etcd's metrics listener off. The chart cannot change that; a machine
// configuration change can, and it arrives through a converge rather than
// through Flux. So the two halves live in different files, read by different
// tools, applied by different mechanisms.
//
// Each half alone fails quietly in its own direction. Enable the chart without
// the machine configuration and three targets sit red, which teaches everyone
// to ignore red targets. Apply the machine configuration without the chart and
// three ports are open on every control-plane node with nothing reading them -
// including etcd's unauthenticated one.
//
// The estate ran the first half of that for a while: the stack shipped with
// these switched off and a comment promising the machine configuration later.
func TestScrapingTheControlPlaneChangesBothHalvesTogether(t *testing.T) {
	stack := readRepoFile(t, "clusters/management/infrastructure/controllers/kube-prometheus-stack.yaml")
	talos := readRepoFile(t, "management/cluster/talos.tf")

	// What the chart is told to scrape, and what the machine configuration
	// has to contain for that scrape to reach anything.
	for _, c := range []struct {
		component string
		exposedBy string
		what      string
	}{
		{"kubeScheduler", `bind-address`, "the scheduler binds to 127.0.0.1 until bind-address says otherwise"},
		{"kubeControllerManager", `bind-address`, "the controller-manager binds to 127.0.0.1 until bind-address says otherwise"},
		{"kubeEtcd", `listen-metrics-urls`, "etcd serves no metrics until listen-metrics-urls asks for them"},
	} {
		enabled := chartComponentEnabled(t, stack, c.component)
		// The assignment, not the word: a key renamed to something Talos
		// ignores still contains the word, and that is precisely the change
		// that would leave the target red.
		exposed := regexp.MustCompile(regexp.QuoteMeta(c.exposedBy) + `\s*=`).MatchString(talos)

		switch {
		case enabled && !exposed:
			t.Errorf("the chart scrapes %s, and management/cluster/talos.tf does not contain %q.\n\n"+
				"%s, so the target would sit red from the moment this deploys - and a red target "+
				"nobody can fix is how people learn to ignore red targets.",
				c.component, c.exposedBy, c.what)
		case !enabled && exposed:
			t.Errorf("management/cluster/talos.tf contains %q and the chart does not scrape %s.\n\n"+
				"That leaves a port open on every control-plane node with nothing reading it. If the "+
				"scrape is being removed deliberately, remove the machine configuration with it.",
				c.exposedBy, c.component)
		}
	}
}

// chartComponentEnabled reads the HelmRelease's values rather than grepping,
// because `enabled: true` appears a dozen times in that file and only its
// position says which component it belongs to.
func chartComponentEnabled(t *testing.T, body, component string) bool {
	t.Helper()
	dec := yaml.NewDecoder(strings.NewReader(body))
	for {
		var doc struct {
			Kind string `yaml:"kind"`
			Spec struct {
				Values map[string]struct {
					Enabled *bool `yaml:"enabled"`
				} `yaml:"values"`
			} `yaml:"spec"`
		}
		if dec.Decode(&doc) != nil {
			return false
		}
		if doc.Kind != "HelmRelease" {
			continue
		}
		c, ok := doc.Spec.Values[component]
		if !ok {
			t.Fatalf("the monitoring HelmRelease says nothing about %s at all, so this guard is "+
				"reading the wrong shape and proves nothing", component)
		}
		return c.Enabled != nil && *c.Enabled
	}
}
