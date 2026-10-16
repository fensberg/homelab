package repo

import (
	"os"
	"path/filepath"
	"testing"
)

// Flux syncs a public repository, so it needs no credential at all. That the
// sync names no secret and the config template declares no token is a
// configuration rule (policy/conftest/gitops.rego), which says why.

// The other half. A secret nothing references is still a secret in state, so
// the OpenTofu side is checked independently of the manifest.
var gitCredentialTokens = []string{
	"source_control.token",
	"source_control/token",
	"flux_system_git_auth",
}

func TestNoGitCredentialIsCreatedForFlux(t *testing.T) {
	root := repoRoot(t)

	for _, rel := range openTofuSources(t) {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		if tok, hit := firstMatch(string(body), gitCredentialTokens); hit {
			t.Errorf(`%s references %q.

Anything OpenTofu reads is a value OpenTofu writes to state. A source-control
token there is a live credential recoverable from a leaked state file, and
Flux does not need one to clone a public repository over https.`, rel, tok)
		}
	}
}
