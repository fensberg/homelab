package repo

import (
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"homelab/tests/parsed"
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

// cniImages is every image the rendered CNI manifest runs: each value
// under an image key, in every document of the file.
func cniImages(t *testing.T) []string {
	t.Helper()
	_, docs := fluxObject(t, cniKind, cniName)
	var out []string
	for _, v := range parsed.Scalars(docs) {
		if v.Key() == "image" {
			out = append(out, v.Value)
		}
	}
	return out
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
	talosFile := declaring(t, declMachineConfig).File
	items := objectItems(tofuFile(t, talosFile).Body)
	sets := func(key, inner string, want cty.Value) bool {
		return slices.ContainsFunc(items, func(item hclsyntax.ObjectConsItem) bool {
			if parsed.KeyName(item) != key {
				return false
			}
			v, ok := parsed.Literal(parsed.Key(item.ValueExpr, inner))
			return ok && !v.IsNull() && v.Type() == want.Type() && v.Equals(want).True()
		})
	}
	if !sets("cni", "name", cty.StringVal("none")) {
		t.Errorf("%s: the cluster does not set cluster.network.cni.name = \"none\".\n\n"+
			"Talos would then install Flannel, which enforces no NetworkPolicy - the "+
			"whole reason this estate moved to Cilium. Two CNIs would also both claim "+
			"pod networking.", talosFile)
	}
	if !sets("proxy", "disabled", cty.True) {
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
	tree := repository(t)
	talosFile := declaring(t, declMachineConfig).File
	// Every collection of machine patches the file's locals declare, less the
	// fragments another collection is made from.
	collections := map[string]*hclsyntax.Attribute{}
	for _, b := range tree.Blocks(t, "locals") {
		if b.File != talosFile {
			continue
		}
		for name, a := range b.Body.Attributes {
			if strings.HasSuffix(name, "patches") {
				collections[name] = a
			}
		}
	}
	var sets []string
	for name := range collections {
		fragment := false
		for other, a := range collections {
			fragment = fragment || (other != name && refersTo(a.Expr, "local."+name))
		}
		if !fragment {
			sets = append(sets, name)
		}
	}
	sort.Strings(sets)
	if len(sets) == 0 {
		t.Fatalf("found no machine-patch collection in %s, so this checked nothing", talosFile)
	}
	for _, name := range sets {
		expr := collections[name].Expr
		if !slices.ContainsFunc(objectItems(expr), func(item hclsyntax.ObjectConsItem) bool { return parsed.KeyName(item) == "kubePrism" }) {
			t.Errorf("%s: %s never declares KubePrism.\n\n"+
				"With kube-proxy gone there is no ClusterIP for these machines' agents to "+
				"reach the API through, and the alternative address is one control plane "+
				"(#316). It is enabled by default, which is exactly why it is written "+
				"down: a default is not a declaration.", talosFile, name)
			continue
		}
		if !writesNumber(expr, 7445) {
			t.Errorf("%s: %s declares KubePrism without naming port 7445.\n\n"+
				"The Cilium values in %s point k8sServicePort at 7445. If the port moves, "+
				"every half has to move with it.", talosFile, name, cniValuesPath(t))
		}
	}
}

// writesNumber says whether a number is written out anywhere in a node.
func writesNumber(n hclsyntax.Node, want int64) bool {
	found := false
	_ = hclsyntax.VisitAll(n, func(n hclsyntax.Node) hcl.Diagnostics {
		if lit, ok := n.(*hclsyntax.LiteralValueExpr); ok && lit.Val.Type() == cty.Number && lit.Val.Equals(cty.NumberIntVal(want)).True() {
			found = true
		}
		return nil
	})
	return found
}

func TestTheRenderedCNIManifestMatchesThePinnedChartVersion(t *testing.T) {
	heavy(t, "semgrep reads the rendered file's header, a few seconds")
	want := pinned(t, cniVersion)

	if !strings.Contains(header(t, cniPath(t)), "cilium "+want) {
		t.Errorf("%s: its provenance header does not name chart version %s.\n\n"+
			"Re-render it with `task render-cni`.", cniPath(t), want)
	}

	// cilium-envoy carries its own upstream version and is deliberately not
	// checked here; the agent and the operator are the chart's own images.
	tagged := regexp.MustCompile(`^quay\.io/cilium/(cilium|operator-generic):v([0-9][^@]*)(@sha256:[0-9a-f]{64})?$`)
	found := 0
	var images [][]string
	for _, image := range cniImages(t) {
		if m := tagged.FindStringSubmatch(image); m != nil {
			images = append(images, m)
		}
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
