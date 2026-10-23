package repo

import (
	"homelab/tests/parsed"
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
// bootstrap and the health gate". That the health gate waits for it, that its
// agent reaches every node and that its MTU is pinned are configuration rules
// (policy/conftest/cni.rego); what is left here is the machine configuration
// and the render.

const cniVersion = "CILIUM_VERSION"

// The rendered CNI manifest, found by the agent it declares, and the values it
// was rendered from, which sit beside it.
func cniPath(t *testing.T) string {
	t.Helper()
	path, _ := fluxObject(t, cniKind, cniName)
	return path
}

// cniValues is every value the rendered CNI manifest holds.
func cniValues(t *testing.T) []string {
	t.Helper()
	_, docs := fluxObject(t, cniKind, cniName)
	return parsed.Values(docs)
}

func cniValuesPath(t *testing.T) string {
	t.Helper()
	return beside(cniPath(t), "cilium-values.yaml")
}

// The built-in CNI and kube-proxy must both be off, and off together.
//
// These are two settings and one decision. Talos ships Flannel and kube-proxy;
// Cilium replaces both. Disabling one without the other leaves either two CNIs
// fighting over pod networking, or kube-proxy and Cilium both programming
// service routing - and the second is the quieter of the two failures.
func TestTheClusterDeclaresNoBuiltInCNIAndNoKubeProxy(t *testing.T) {
	talosFile, body := tofuDeclaring(t, declMachineConfig)

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
	talosFile, body := tofuDeclaring(t, declMachineConfig)

	// Every collection of machine patches that is a machine's whole list, so
	// a machine class added later cannot quietly skip it. There were two
	// such lists, a zone's and everybody else's, and this judged each. There
	// is one now, which every kind of machine is configured from
	// (TestEveryKindOfMachineIsConfiguredFromTheOneList holds that), and a
	// second collection is a fragment added to it: what makes a zone's
	// machine the zone's. A fragment declares nothing of its own and is told
	// from a list by being one the whole list draws on.
	all := regexp.MustCompile(`(?m)^  ([a-z_]*patches) = \{`).FindAllStringSubmatch(body, -1)
	allBlocks := regexp.MustCompile(`(?m)^  [a-z_]*patches = \{`).Split(body, -1)[1:]
	var sets [][]string
	var blocks []string
	for i, name := range all {
		fragment := false
		for j, other := range allBlocks {
			fragment = fragment || (j != i && strings.Contains(other, "local."+name[1]+"["))
		}
		if !fragment {
			sets, blocks = append(sets, name), append(blocks, allBlocks[i])
		}
	}
	if len(sets) == 0 {
		t.Fatalf("found no machine-patch collection in %s, so this checked nothing", talosFile)
	}

	// Each is judged on its own contents.
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
				"every half has to move with it.", talosFile, name[1], cniValuesPath(t))
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
	heavy(t, "semgrep reads the rendered file's header, a few seconds")
	want := pinned(t, cniVersion)

	if !strings.Contains(header(t, cniPath(t)), "cilium "+want) {
		t.Errorf("%s: its provenance header does not name chart version %s.\n\n"+
			"Re-render it with `task render-cni`.", cniPath(t), want)
	}

	// cilium-envoy carries its own upstream version and is deliberately not
	// checked here; the agent and the operator are the chart's own images.
	tagged := regexp.MustCompile(`quay\.io/cilium/(cilium|operator-generic):v([0-9][^@"\s]*)`)
	found := 0
	var images [][]string
	for _, v := range cniValues(t) {
		images = append(images, tagged.FindAllStringSubmatch(v, -1)...)
	}
	for _, m := range images {
		found++
		if m[2] != want {
			t.Errorf("%s: image %s is tagged v%s, but %s pins %s.\n\n"+
				"The manifest was rendered from a different chart than the one this "+
				"repository claims to run. Re-render it with `task render-cni`.",
				cniPath(t), m[1], m[2], cniVersion, want)
		}
	}
	if found == 0 {
		t.Fatalf("%s contains no quay.io/cilium image references, so this test proves nothing.\n\n"+
			"Either the manifest is empty or the images moved registry.", cniPath(t))
	}
}
