package repo

import (
	"regexp"
	"strings"
	"testing"

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
// (policy/conftest/cni.rego); that every machine turns the built-in CNI and
// kube-proxy off and declares KubePrism on Cilium's port are rules over the
// plan (policy/conftest/plan_builtin_cni_off.rego, plan_kubeprism.rego). What
// is left here is the render.

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
