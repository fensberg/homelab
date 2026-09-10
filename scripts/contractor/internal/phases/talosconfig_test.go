package phases

import (
	"strings"
	"testing"
)

// The shape check exists to fail here, naming the output, rather than several
// commands later inside talosctl with an error about control characters.
//
// It matters more than the kubeconfig equivalent because of what comes back
// when state cannot be reached: tofu answers with a diagnostic, and a
// diagnostic written to a file that a reboot-capable credential is read from
// is a failure nobody wants to debug under pressure.
func TestLooksLikeTalosconfig(t *testing.T) {
	valid := "context: site0\ncontexts:\n  site0:\n    endpoints:\n      - 10.10.10.100\n"

	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"a talosconfig", valid, true},
		{"empty", "", false},
		{"whitespace only", "   \n\t\n", false},
		{"a kubeconfig is not a talosconfig", "apiVersion: v1\nclusters:\n users:\n", false},
		{"a tofu diagnostic", "Warning: No outputs found\n", false},
		{"missing the contexts map", "context: site0\n", false},
		{"binary junk", "context:\ncontexts:\n\x00\x01", false},
	}

	for _, tc := range cases {
		if got := looksLikeTalosconfig(tc.raw); got != tc.want {
			t.Errorf("%s: looksLikeTalosconfig = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The two credentials must not be interchangeable. Each check has to reject
// the other's output, or a wrong-output read writes a plausible-looking file
// and the failure surfaces somewhere useless.
func TestTheTwoCredentialChecksDoNotOverlap(t *testing.T) {
	talos := "context: site0\ncontexts:\n  site0:\n    endpoints: []\n"
	kube := "apiVersion: v1\nclusters: []\nusers: []\n"

	if looksLikeKubeconfig(talos) {
		t.Error("a talosconfig was accepted as a kubeconfig")
	}
	if looksLikeTalosconfig(kube) {
		t.Error("a kubeconfig was accepted as a talosconfig")
	}
}

// A talosconfig that names no nodes is well-formed and useless: every
// node-targeted command refuses on first use. These cases are the shapes the
// Talos provider and a hand-edit actually produce, rather than invented ones.
func TestNamesNodes(t *testing.T) {
	const withNodes = `context: site0
contexts:
  site0:
    endpoints:
      - 192.0.2.100
    nodes:
      - 192.0.2.100
      - 192.0.2.101
`
	const noNodesField = `context: site0
contexts:
  site0:
    endpoints:
      - 192.0.2.100
`
	const emptyBlock = `context: site0
contexts:
  site0:
    endpoints:
      - 192.0.2.100
    nodes:
`
	const emptyInline = `context: site0
contexts:
  site0:
    nodes: []
`
	const inline = `context: site0
contexts:
  site0:
    nodes: [192.0.2.100]
`
	// A key that merely ends in "nodes" is not the nodes field. Without the
	// prefix check this passes on a credential that names nothing.
	const lookalike = `context: site0
contexts:
  site0:
    worker_nodes:
      - 192.0.2.200
`
	for _, tc := range []struct {
		name string
		raw  string
		ok   bool
	}{
		{"a block sequence of nodes", withNodes, true},
		{"an inline list", inline, true},
		{"no nodes field at all", noNodesField, false},
		{"nodes with nothing under it", emptyBlock, false},
		{"an empty inline list", emptyInline, false},
		{"a field that only ends in nodes", lookalike, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := namesNodes(tc.raw)
			if tc.ok && err != nil {
				t.Fatalf("refused a usable talosconfig: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("accepted a talosconfig on which every node command would refuse")
			}
		})
	}
}

// The refusal has to say where to fix it. A guard that fires without naming
// the file it is about turns a one-line change into a search.
func TestNamesNodesSaysWhereToFixIt(t *testing.T) {
	err := namesNodes("context: site0\ncontexts:\n  site0:\n")
	if err == nil {
		t.Fatal("no refusal to inspect")
	}
	for _, want := range []string{"management/cluster/talos.tf", "nodes", "-n"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal never mentions %q, so it does not say what to do:\n%s", want, err)
		}
	}
}
