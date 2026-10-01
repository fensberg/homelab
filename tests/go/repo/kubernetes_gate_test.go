package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"homelab/contractor/config"
	"homelab/contractor/steps"
)

// Nothing may talk to Kubernetes before the cluster is known to be up, and
// nothing that lives inside the cluster may be in the root a teardown destroys.
//
// WHAT THIS GUARDS, and it is not style. A kubernetes_* resource planned
// alongside the machines starts in the first wave of the apply, while the API
// server is still pulling its static pods; the provider gets `connection
// refused` and an ignition tears the estate back down. That happened:
// `kubernetes_namespace.valheim` and `kubernetes_secret.runner_vars` went that
// way in one run, thirty seconds after bootstrap. And on the way down, deleting
// the flux-system namespace hung on Flux's finalizers until the destroy gave up
// having destroyed nothing, the machines included.
//
// Both used to be held off resource by resource: a depends_on on the cluster's
// health for each namespace, and a teardown step that forgot every kubernetes_*
// address out of state. A new resource that left either out reintroduced the
// failure, and the provider itself was configured from a resource in its own
// root, so no import or plan could resolve it before the cluster stood.
//
// Now it is the shape of the site (docs/epochs/02-abstraction.md, "Inside a
// site, the split is two roots sharing one config"). Everything that talks to
// the cluster's API is in the platform root, which is applied only after the
// cluster root's last step has returned - the step that waits for every node
// to be Ready - and is never destroyed, because what it describes goes with
// the machines' disks. So there is no edge to forget. What can still go wrong
// is a resource in the wrong root, or the order of the steps, and those are
// what this checks.
func TestKubernetesIsOnlyInThePlatformRootAndThatRootComesLast(t *testing.T) {
	root := repoRoot(t)
	files := tracked(t, func(rel string) bool {
		return strings.HasSuffix(rel, ".tf") && !strings.Contains(rel, "/tests/")
	})
	sources := map[string]string{}
	for _, rel := range files {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		sources[rel] = string(body)
	}
	for _, p := range kubernetesOutsideThePlatform(sources) {
		t.Error(p)
	}
	for _, p := range stepOrderProblems(steps.Converge) {
		t.Error(p)
	}

	// The gate itself: the cluster root's last step applies everything in
	// it, and this is what makes "applied" mean "every node is Ready".
	talos := sources["management/"+config.ClusterRoot+"/talos.tf"]
	if !strings.Contains(talos, `data "talos_cluster_health" "this"`) {
		t.Errorf("management/%s/talos.tf no longer reads the cluster's health, so the %s root's last step returns before the nodes are Ready and the %s root is applied to a cluster that cannot schedule anything", config.ClusterRoot, config.ClusterRoot, config.PlatformRoot)
	}
}

var (
	kubernetesBlock = regexp.MustCompile(`(?m)^\s*(resource|data)\s+"(kubernetes_[A-Za-z0-9_]+)"\s+"([A-Za-z0-9_]+)"`)
	// The provider block, or the provider in required_providers.
	kubernetesProvider = regexp.MustCompile(`(?m)^\s*provider\s+"kubernetes"|^\s*kubernetes\s*=\s*\{`)
)

// kubernetesOutsideThePlatform is every kubernetes resource or provider
// declared anywhere but the platform root, and the complaint that there are
// none in the platform root at all, which would mean this looked in the wrong
// place.
func kubernetesOutsideThePlatform(sources map[string]string) []string {
	platform := "management/" + config.PlatformRoot + "/"
	var problems []string
	inPlatform := 0
	for rel, body := range sources {
		code := stripHCLComments(body)
		for _, m := range kubernetesBlock.FindAllStringSubmatch(code, -1) {
			if strings.HasPrefix(rel, platform) {
				inPlatform++
				continue
			}
			problems = append(problems, rel+" declares "+m[2]+"."+m[3]+`. Everything that talks to the cluster's API belongs in `+platform+`: it is applied only after every node is Ready, and it is never destroyed. Here it would be planned before the cluster exists, and a teardown would wait on the cluster to delete it.`)
		}
		if !strings.HasPrefix(rel, platform) && kubernetesProvider.MatchString(code) {
			problems = append(problems, rel+" configures or requires the kubernetes provider outside "+platform+". A provider configured in the root that builds the cluster cannot be resolved until the cluster exists, so every import and every plan before that fails on it.")
		}
	}
	if inPlatform == 0 {
		problems = append(problems, "no kubernetes resource was found in "+platform+", so this guard is looking in the wrong place")
	}
	sort.Strings(problems)
	return problems
}

// stripHCLComments drops each line's comment, so a resource described in one
// is not read as a resource.
func stripHCLComments(body string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i], _, _ = strings.Cut(line, "#")
	}
	return strings.Join(lines, "\n")
}

// stepOrderProblems is what is wrong with the order of the converge's steps:
// every cluster step must come before every platform step, and the cluster
// root must end with its untargeted apply.
func stepOrderProblems(converge []steps.Step) []string {
	var problems []string
	lastCluster, firstPlatform := -1, -1
	for i, st := range converge {
		switch st.Root {
		case config.ClusterRoot:
			lastCluster = i
		case config.PlatformRoot:
			if firstPlatform < 0 {
				firstPlatform = i
			}
		default:
			problems = append(problems, "step "+st.Label+" names the root "+st.Root+", which is not one of a site's")
		}
	}
	switch {
	case lastCluster < 0 || firstPlatform < 0:
		problems = append(problems, "the converge does not apply both of a site's roots, so one of them is never built")
	case firstPlatform < lastCluster:
		problems = append(problems, "step "+converge[firstPlatform].Label+" applies the platform root before step "+converge[lastCluster].Label+" has finished the cluster root. The platform is applied to a cluster whose nodes are Ready, and only the cluster root's last step makes that true.")
	case len(converge[lastCluster].Targets) != 0:
		problems = append(problems, "the cluster root's last step, "+converge[lastCluster].Label+", is targeted. Only an untargeted apply includes the health read that waits for every node, so the platform root would be applied to a cluster that is not ready.")
	}
	return problems
}

func TestKubernetesOutsideThePlatformIsRefused(t *testing.T) {
	platform := "management/" + config.PlatformRoot + "/"
	good := map[string]string{
		platform + "a.tf":             "resource \"kubernetes_namespace\" \"a\" {}\n",
		platform + "versions.tf":      "provider \"kubernetes\" {}\n",
		"management/cluster/talos.tf": "# resource \"kubernetes_namespace\" \"commented\" {}\n  kubernetes_version = local.kubernetes_version\n",
	}
	if got := kubernetesOutsideThePlatform(good); len(got) != 0 {
		t.Errorf("a site with kubernetes only in the platform root was refused: %v", got)
	}
	for name, add := range map[string]string{
		"a resource in the cluster root":    "resource \"kubernetes_secret\" \"s\" {}\n",
		"a data source in the cluster root": "data \"kubernetes_namespace\" \"n\" {}\n",
		"the provider in the cluster root":  "provider \"kubernetes\" {}\n",
		"the provider required there":       "    kubernetes = { source = \"hashicorp/kubernetes\" }\n",
	} {
		bad := map[string]string{"management/cluster/extra.tf": add}
		for k, v := range good {
			bad[k] = v
		}
		if got := kubernetesOutsideThePlatform(bad); len(got) != 1 {
			t.Errorf("%s: got %v", name, got)
		}
	}
	if got := kubernetesOutsideThePlatform(map[string]string{"management/cluster/talos.tf": ""}); len(got) != 1 || !strings.Contains(got[0], "wrong place") {
		t.Errorf("a platform root with nothing in it was accepted: %v", got)
	}
}

func TestStepOrderProblemsRefusesThePlatformBeforeTheClusterIsReady(t *testing.T) {
	c, p := config.ClusterRoot, config.PlatformRoot
	for name, tc := range map[string]struct {
		steps []steps.Step
		want  string
	}{
		"the converge's own":        {steps.Converge, ""},
		"platform first":            {[]steps.Step{{Root: p, Label: "p"}, {Root: c, Label: "c"}}, "before step"},
		"cluster ends targeted":     {[]steps.Step{{Root: c, Label: "c", Targets: []string{"x"}}, {Root: p, Label: "p"}}, "is targeted"},
		"no platform":               {[]steps.Step{{Root: c, Label: "c"}}, "both of a site's roots"},
		"a root that is not a root": {[]steps.Step{{Root: c, Label: "c"}, {Root: p, Label: "p"}, {Root: "elsewhere", Label: "e"}}, "not one of a site's"},
	} {
		got := strings.Join(stepOrderProblems(tc.steps), "|")
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}
