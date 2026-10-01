package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
	// it, and the health read is what makes "applied" mean "every node is
	// Ready". Found by what it declares; all that matters about where is that
	// it is not in the root that waits on it.
	health, _ := tofuDeclaring(t, declClusterHealth)
	if _, places := platformPlaces(sources); slices.Contains(places, filepath.ToSlash(filepath.Dir(health))) {
		t.Errorf("%s reads the cluster's health from the %s root, which is applied after the %s root has returned: nothing then waits for the nodes before the platform is put on them", health, config.PlatformRoot, config.ClusterRoot)
	}
}

var (
	kubernetesBlock         = regexp.MustCompile(`(?m)^\s*(resource|data)\s+"(kubernetes_[A-Za-z0-9_]+)"\s+"([A-Za-z0-9_]+)"`)
	kubernetesProviderBlock = regexp.MustCompile(`(?m)^\s*provider\s+"kubernetes"`)
	kubernetesRequired      = regexp.MustCompile(`(?m)^\s*kubernetes\s*=\s*\{`)
)

var moduleSource = regexp.MustCompile(`(?m)^\s*source\s*=\s*"(\.[^"]*)"`)

// platformPlaces is where the platform keeps what it creates: its root, and
// every module that root calls by a local path. Read from the root rather
// than named, so the platform's resources are found wherever the root says
// they are.
func platformPlaces(sources map[string]string) (root string, places []string) {
	root = "management/" + config.PlatformRoot
	places = []string{root}
	for rel, body := range sources {
		if filepath.ToSlash(filepath.Dir(rel)) != root {
			continue
		}
		for _, m := range moduleSource.FindAllStringSubmatch(stripHCLComments(body), -1) {
			// A site's root reads its modules from its pinned tree; the
			// module itself is at the same path from the repository's top.
			if pinned := pinnedSource.FindStringSubmatch(m[1]); pinned != nil {
				places = append(places, filepath.ToSlash(filepath.Clean(pinned[1])))
				continue
			}
			places = append(places, filepath.ToSlash(filepath.Join(root, m[1])))
		}
	}
	sort.Strings(places)
	return root, places
}

// kubernetesOutsideThePlatform is every kubernetes resource declared anywhere
// but the platform - its root or a module that root calls - every provider
// block configured anywhere but the platform's root, and the complaint that
// the platform holds no kubernetes resource at all, which would mean this
// looked in the wrong place.
func kubernetesOutsideThePlatform(sources map[string]string) []string {
	root, places := platformPlaces(sources)
	inPlatform := func(rel string) bool {
		return slices.Contains(places, filepath.ToSlash(filepath.Dir(rel)))
	}
	var problems []string
	found := 0
	for rel, body := range sources {
		code := stripHCLComments(body)
		for _, m := range kubernetesBlock.FindAllStringSubmatch(code, -1) {
			if inPlatform(rel) {
				found++
				continue
			}
			problems = append(problems, rel+" declares "+m[2]+"."+m[3]+`. Everything that talks to the cluster's API belongs to the platform (`+strings.Join(places, ", ")+`): it is applied only after every node is Ready, and it is never destroyed. Here it would be planned before the cluster exists, and a teardown would wait on the cluster to delete it.`)
		}
		if kubernetesProviderBlock.MatchString(code) && filepath.ToSlash(filepath.Dir(rel)) != root {
			problems = append(problems, rel+" configures the kubernetes provider outside "+root+". A provider is configured once, in the root that is handed the cluster's access; configured where the cluster is built, it cannot be resolved until the cluster exists.")
		}
		if kubernetesRequired.MatchString(code) && !inPlatform(rel) {
			problems = append(problems, rel+" requires the kubernetes provider outside the platform ("+strings.Join(places, ", ")+"), so something there means to talk to a cluster that may not exist yet.")
		}
	}
	if found == 0 {
		problems = append(problems, "no kubernetes resource was found in the platform ("+strings.Join(places, ", ")+"), so this guard is looking in the wrong place")
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
	platform, cluster := "management/"+config.PlatformRoot+"/", "management/"+config.ClusterRoot+"/"
	// The platform's root calls one module, and that is where its resources
	// are; the cluster's calls another.
	good := map[string]string{
		platform + "calls.tf":  "module \"p\" {\n  source = \"../../parts/on-it\"\n}\n",
		platform + "access.tf": "provider \"kubernetes\" {}\n    kubernetes = { source = \"hashicorp/kubernetes\" }\n",
		"parts/on-it/a.tf":     "resource \"kubernetes_namespace\" \"a\" {}\n    kubernetes = { source = \"hashicorp/kubernetes\" }\n",
		cluster + "calls.tf":   "module \"c\" {\n  source = \"../../parts/machines\"\n}\n",
		"parts/machines/n.tf":  "# resource \"kubernetes_namespace\" \"commented\" {}\n  kubernetes_version = local.kubernetes_version\n",
	}
	if got := kubernetesOutsideThePlatform(good); len(got) != 0 {
		t.Errorf("a site with kubernetes only in the platform was refused: %v", got)
	}
	for name, tc := range map[string]struct{ file, add string }{
		"a resource in the cluster root":         {cluster + "extra.tf", "resource \"kubernetes_secret\" \"s\" {}\n"},
		"a resource in the cluster's module":     {"parts/machines/extra.tf", "resource \"kubernetes_secret\" \"s\" {}\n"},
		"a data source in the cluster's module":  {"parts/machines/extra.tf", "data \"kubernetes_namespace\" \"n\" {}\n"},
		"a resource in a module nothing calls":   {"parts/stray/extra.tf", "resource \"kubernetes_secret\" \"s\" {}\n"},
		"the provider configured in the cluster": {cluster + "extra.tf", "provider \"kubernetes\" {}\n"},
		"the provider configured in the module":  {"parts/on-it/extra.tf", "provider \"kubernetes\" {}\n"},
		"the provider required by the cluster":   {"parts/machines/extra.tf", "    kubernetes = { source = \"hashicorp/kubernetes\" }\n"},
	} {
		bad := map[string]string{tc.file: tc.add}
		for k, v := range good {
			bad[k] = v
		}
		if got := kubernetesOutsideThePlatform(bad); len(got) != 1 {
			t.Errorf("%s: got %v", name, got)
		}
	}
	if got := kubernetesOutsideThePlatform(map[string]string{cluster + "calls.tf": ""}); len(got) != 1 || !strings.Contains(got[0], "wrong place") {
		t.Errorf("a platform with nothing in it was accepted: %v", got)
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
