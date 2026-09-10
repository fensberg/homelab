package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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

	if !strings.Contains(body, "kubePrism") {
		t.Fatalf("%s: KubePrism is never declared.\n\n"+
			"It is enabled by default today, which is exactly why it is written down: "+
			"a default is not a declaration, and the pod network depends on this one.",
			talosFile)
	}
	if !strings.Contains(body, "7445") {
		t.Errorf("%s: KubePrism is declared without naming port 7445.\n\n"+
			"The Cilium values in %s point k8sServicePort at 7445. If the port moves, "+
			"both halves have to move together.", talosFile, cniValues)
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
