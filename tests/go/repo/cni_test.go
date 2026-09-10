package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The CNI this estate runs is not the one Talos ships, and four things have to
// stay true at once for that to work. Each is one line somewhere, each is
// silent when wrong, and three of the four fail as a ten-minute hang rather
// than as an error.
//
// See docs/epochs/03-workload.md, "Cilium arrives from OpenTofu, between
// bootstrap and the health gate".

const (
	talosFile  = "management/cluster/talos.tf"
	cniApply   = "terraform_data.cilium"
	cniMani    = "clusters/bootstrap/cilium.yaml"
	cniValues  = "clusters/bootstrap/cilium-values.yaml"
	cniVersion = "CILIUM_VERSION"
)

// hclBlock returns the text of the top-level block whose header line starts
// with prefix, from that line to the closing brace in column zero.
//
// Regex over HCL is crude and is chosen deliberately over parsing: the
// alternative is a third-party HCL library in a module whose whole point is
// that it has no third-party anything.
func hclBlock(t *testing.T, body, prefix string) string {
	t.Helper()
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if lines[j] == "}" {
				return strings.Join(lines[i:j+1], "\n")
			}
		}
		t.Fatalf("block %q is never closed by a brace in column zero", prefix)
	}
	t.Fatalf("no top-level block starting %q found.\n\n"+
		"If it was renamed, this test needs to move with it - it is asserting an "+
		"ordering constraint, not a spelling.", prefix)
	return ""
}

// Nothing may ask whether the cluster is healthy before the CNI is installed.
//
// A node with no CNI never reaches Ready. `data.talos_cluster_health` waits for
// every node to be Ready and is given a ten-minute timeout, so removing this
// dependency does not produce an error - it produces an ignition that appears
// to hang, ten minutes from the phase that was actually at fault.
//
// This is the single edge holding the sequence together: bootstrap, apply the
// CNI, nodes go Ready, gate passes. It is one line, and deleting it is the kind
// of tidy-up that looks harmless in a diff.
func TestTheHealthGateWaitsForTheCNI(t *testing.T) {
	block := hclBlock(t, readRepoFile(t, talosFile), `data "talos_cluster_health"`)

	if !strings.Contains(block, cniApply) {
		t.Errorf("%s: the health gate does not depend on %s.\n\n"+
			"Talos is configured with no built-in CNI, so no node reaches Ready until "+
			"Cilium is applied. Without this edge the gate starts first and waits its "+
			"full ten-minute timeout before failing, blaming the cluster rather than "+
			"the missing dependency.\n\nAdd %s to its depends_on.",
			talosFile, cniApply, cniApply)
	}
}

// The built-in CNI and kube-proxy must both be off, and off together.
//
// These are two settings and one decision. Talos ships Flannel and kube-proxy;
// Cilium replaces both. Disabling one without the other leaves either two CNIs
// fighting over pod networking, or kube-proxy and Cilium both programming
// service routing - and the second is the quieter of the two failures.
func TestTheClusterDeclaresNoBuiltInCNIAndNoKubeProxy(t *testing.T) {
	body := readRepoFile(t, talosFile)

	// yamlencode renders these as nested YAML keys, so assert on the HCL that
	// produces them rather than on rendered output nobody can see from here.
	cniOff := regexp.MustCompile(`(?s)cni\s*=\s*\{[^}]*name\s*=\s*"none"`)
	proxyOff := regexp.MustCompile(`(?s)proxy\s*=\s*\{[^}]*disabled\s*=\s*true`)

	if !cniOff.MatchString(body) {
		t.Errorf("%s: the cluster does not set cluster.network.cni.name = \"none\".\n\n"+
			"Talos would then install Flannel, which enforces no NetworkPolicy - the "+
			"whole reason this estate moved to Cilium. Two CNIs would also both claim "+
			"pod networking.", talosFile)
	}
	if !proxyOff.MatchString(body) {
		t.Errorf("%s: the cluster does not set cluster.proxy.disabled = true.\n\n"+
			"Cilium is configured with kubeProxyReplacement, so leaving kube-proxy "+
			"running means two things programming service routing on every node.",
			talosFile)
	}
}

// KubePrism is declared even though Talos enables it by default.
//
// With kube-proxy gone, every Cilium agent reaches the API server through
// KubePrism on localhost:7445. That is load-bearing: the alternative address is
// the cluster endpoint, which this estate hardcodes to one control plane
// (#316), and using it would turn a known API single point of failure into a
// pod-network one.
//
// Relying on an upstream default for that is a blind spot. The day it changes,
// the pod network fails and nothing in this repository ever said it was
// required.
func TestKubePrismIsDeclaredRatherThanAssumed(t *testing.T) {
	body := readRepoFile(t, talosFile)

	// Every collection of machine patches, so a machine class added later
	// cannot quietly skip it. This used to assert that the file mentioned
	// KubePrism somewhere, which was true and insufficient the moment a second
	// patch set existed: the untrusted zone's agents need the endpoint exactly
	// as much as a worker's do, and a check satisfied by somebody else's
	// declaration would not have noticed theirs missing.
	sets := regexp.MustCompile(`(?m)^  ([a-z_]*patches) = \{`).FindAllStringSubmatch(body, -1)
	if len(sets) < 2 {
		t.Fatalf("found %d machine-patch collection(s) in %s; with fewer than two this "+
			"cannot detect one being left out.", len(sets), talosFile)
	}

	// Split on the collection headers so each is judged on its own contents.
	blocks := regexp.MustCompile(`(?m)^  [a-z_]*patches = \{`).Split(body, -1)[1:]
	for i, name := range sets {
		block := blocks[i]
		if !strings.Contains(block, "kubePrism") {
			t.Errorf("%s: %s never declares KubePrism.\n\n"+
				"With kube-proxy gone there is no ClusterIP for these machines' agents to "+
				"reach the API through, and the alternative address is one control plane "+
				"(#316). It is enabled by default, which is exactly why it is written "+
				"down: a default is not a declaration.", talosFile, name[1])
			continue
		}
		if !strings.Contains(block, "7445") {
			t.Errorf("%s: %s declares KubePrism without naming port 7445.\n\n"+
				"The Cilium values in %s point k8sServicePort at 7445. If the port moves, "+
				"every half has to move with it.", talosFile, name[1], cniValues)
		}
	}
}

// The committed manifest must be the one the pinned chart version renders.
//
// The manifest is generated by `task render-cni` and committed so its image
// digests are reviewable and the supplier guard can read them. A generated file
// that nobody can prove is current is worse than no file: it silently becomes
// the source of truth while claiming to be a copy.
//
// The header alone would be a change detector - a comment anyone can edit. The
// image tags are the evidence that cannot be faked without changing what
// actually gets deployed, so both are checked and the tags are what would catch
// a bumped version and a forgotten re-render.
func TestTheRenderedCNIManifestMatchesThePinnedChartVersion(t *testing.T) {
	root := repoRoot(t)

	env, err := os.ReadFile(filepath.Join(root, "scripts", "versions.env"))
	if err != nil {
		t.Fatalf("reading versions.env: %v", err)
	}
	pin := regexp.MustCompile(`(?m)^` + cniVersion + `=(.+)$`).FindStringSubmatch(string(env))
	if pin == nil {
		t.Fatalf("scripts/versions.env declares no %s.\n\n"+
			"Every version this estate takes is pinned in that one file.", cniVersion)
	}
	want := strings.TrimSpace(pin[1])

	manifest := readRepoFile(t, cniMani)

	if !strings.Contains(manifest, "cilium "+want) {
		t.Errorf("%s: its provenance header does not name chart version %s.\n\n"+
			"Re-render it with `task render-cni`.", cniMani, want)
	}

	// cilium-envoy carries its own upstream version and is deliberately not
	// checked here; the agent and the operator are the chart's own images.
	tagged := regexp.MustCompile(`quay\.io/cilium/(cilium|operator-generic):v([0-9][^@"\s]*)`)
	found := 0
	for _, m := range tagged.FindAllStringSubmatch(manifest, -1) {
		found++
		if m[2] != want {
			t.Errorf("%s: image %s is tagged v%s, but %s pins %s.\n\n"+
				"The manifest was rendered from a different chart than the one this "+
				"repository claims to run. Re-render it with `task render-cni`.",
				cniMani, m[1], m[2], cniVersion, want)
		}
	}
	if found == 0 {
		t.Fatalf("%s contains no quay.io/cilium image references, so this test proves nothing.\n\n"+
			"Either the manifest is empty or the images moved registry.", cniMani)
	}
}

// Every node gets an agent, including one that is tainted.
//
// This is the DMZ node's readiness, asserted before that node exists.
//
// docs/epochs/03-workload.md puts the untrusted workload on a dedicated worker
// carrying a NoSchedule taint, with the toleration held only by workloads in
// that zone - deliberately, so the scheduler cannot quietly widen the blast
// radius by putting something else there. A CNI is the one thing that must
// ignore that rule: a node with no agent has no pod network, never reaches
// Ready, and never joins the cluster at all.
//
// So the agent DaemonSet tolerating every taint is load-bearing rather than
// sloppy. This estate has refused a blanket `operator: Exists` toleration
// before - on an unpinned debug image proposed for a control plane - and the
// distinction is worth stating: there it was a convenience that widened where
// untrusted code could run, here it is the condition on a machine having a
// network. If a chart version or a values change ever drops it, the DMZ node
// will simply never come up, and nothing else in this repository would say why.
func TestTheCNIReachesEveryNodeIncludingTaintedOnes(t *testing.T) {
	var checked int

	for _, doc := range strings.Split(readRepoFile(t, cniMani), "\n---\n") {
		var d struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Spec struct {
				Template struct {
					Spec struct {
						Tolerations []struct {
							Key      string `yaml:"key"`
							Operator string `yaml:"operator"`
						} `yaml:"tolerations"`
					} `yaml:"spec"`
				} `yaml:"template"`
			} `yaml:"spec"`
		}
		if err := yaml.Unmarshal([]byte(doc), &d); err != nil {
			continue // not a document this test has anything to say about
		}
		if d.Kind != "DaemonSet" || d.Metadata.Name != "cilium" {
			continue
		}
		checked++

		blanket := false
		for _, tol := range d.Spec.Template.Spec.Tolerations {
			// No key plus Exists is "tolerate everything". A keyed toleration
			// covers one taint, which is exactly the regression this catches:
			// it looks correct and silently excludes the zone it was not told
			// about.
			if tol.Key == "" && tol.Operator == "Exists" {
				blanket = true
			}
		}
		if !blanket {
			t.Errorf("%s: the cilium DaemonSet does not tolerate every taint.\n\n"+
				"A tainted node would get no agent, and a node with no CNI never "+
				"reaches Ready - so it would never join the cluster. The untrusted "+
				"zone in docs/epochs/03-workload.md is a NoSchedule-tainted worker, "+
				"so this is the line that lets it exist at all.", cniMani)
		}
	}

	if checked == 0 {
		t.Fatalf("%s contains no DaemonSet named cilium, so this test proves nothing.\n\n"+
			"Either the manifest is empty or the agent was renamed.", cniMani)
	}
}
