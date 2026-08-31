package phases

import (
	"testing"

	"homelab/contractor/internal/run"
)

// The verb read `tofu output` with no state reachable and wrote whatever came
// back. kubectl then failed with "yaml: control characters are not allowed",
// which names the symptom and nothing about the cause. This check fails where
// the mistake is.
func TestLooksLikeKubeconfig(t *testing.T) {
	const real = `apiVersion: v1
kind: Config
clusters:
- name: example
  cluster:
    server: https://192.0.2.100:6443
users:
- name: admin
`
	if !looksLikeKubeconfig(real) {
		t.Error("a real kubeconfig was rejected")
	}

	for _, tc := range []struct{ name, in string }{
		{"empty", ""},
		{"whitespace only", "   \n\t "},
		{"a tofu diagnostic", "╷\n│ Error: Backend initialization required\n╵"},
		{"an ANSI-coloured message", "\x1b[31mError\x1b[0m: no state"},
		{"a control character in otherwise valid yaml", "apiVersion: v1\nclusters: []\nusers: []\n\x00"},
		{"yaml that is not a kubeconfig", "apiVersion: v1\nkind: Secret\n"},
	} {
		if looksLikeKubeconfig(tc.in) {
			t.Errorf("%s was accepted as a kubeconfig; it would be written to disk and fail inside kubectl", tc.name)
		}
	}
}

// Reading an output means attaching, and attaching writes backend_pg.tf and
// tofu's backend record. This verb returns before the sterilize a phase
// sequence would end with, so it has to clean up itself - and it did not,
// which left the backend record behind and broke `tofu init -backend=false`,
// `task validate` and the pre-push hook on a machine that had done nothing but
// look at its own cluster.
//
// The kubeconfig is the one thing it is asked to leave.
func TestKubeconfigCleanupKeepsOnlyTheKubeconfig(t *testing.T) {
	ctx := run.NewContext(t.TempDir(), "site0")

	var keptDest bool
	for _, target := range sterilizeTargets(ctx) {
		if target == ctx.Kubeconfig {
			keptDest = true
		}
	}
	if !keptDest {
		t.Fatal("the kubeconfig is not in the sterilize list, so the cleanup loop has nothing to skip and this test proves nothing")
	}

	// Everything else the verb can create must be swept.
	for _, want := range []string{ctx.BackendPgOn, ctx.TofuBackendRecord, ctx.ConfigRendered} {
		var found bool
		for _, target := range sterilizeTargets(ctx) {
			if target == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is not swept after writing a kubeconfig; it would be left on disk", want)
		}
	}
}
