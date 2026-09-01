package phases

import "testing"

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
