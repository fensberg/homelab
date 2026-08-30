package phases

import "testing"

// The verb read `tofu output` with no state reachable and wrote whatever came
// back. kubectl then failed with "yaml: control characters are not allowed",
// which names the symptom and nothing about the cause. This check fails where
// the mistake is.
func TestLooksLikeKubeconfig(t *testing.T) {
	const real = `apiVersion: v1
kind: Config
clusters:
- name: fensberg
  cluster:
    server: https://10.10.10.100:6443
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
