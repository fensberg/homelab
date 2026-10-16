package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// That the client pins track the cluster, that no workflow restates a pin,
// that no pin is rclone's configuration and that a change is tried with the
// site's scheduler are configuration rules (policy/conftest/versions.rego).

// The pin is only a pin if the thing installing it reads it.
//
// install-dependencies.sh sourced versions.env and then overwrote
// KUBECTL_VERSION with `curl https://dl.k8s.io/release/stable.txt`, so the
// workstation installed whatever upstream called stable that day while the
// runner image honoured the pin. The file whose entire job is making those two
// agree was the one being ignored, and the kubectl pin's rule would have been
// checking a number nothing used. #178.
func TestTheInstallerDoesNotOverrideAPinnedVersion(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "install-dependencies.sh"))
	if err != nil {
		t.Fatalf("reading install-dependencies.sh: %v", err)
	}

	// A pinned variable being assigned from a command substitution is the
	// shape of the defect: sourced from versions.env, then replaced.
	reassign := regexp.MustCompile(`(?m)^\s*([A-Z_]+_VERSION)\s*=\s*"?\$\(`)
	found := reassign.FindAllSubmatch(body, -1)
	if len(found) == 0 {
		return
	}
	for _, m := range found {
		t.Errorf(`install-dependencies.sh assigns %s from a command, overriding the pin
in versions.env.

Sourcing the pin and then replacing it is worse than not pinning at all: the
file still claims to be the one place a version is declared, the runner image
still honours it, and the two quietly install different versions of the same
tool. Read the pin, or take the version out of versions.env and say why.`,
			m[1])
	}
}
