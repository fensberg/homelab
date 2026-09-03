package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// talosctl is a client for a specific Talos version. Sidero's own guidance is
// to keep it within one minor of the cluster it talks to, and the two are
// pinned in different files by different people at different times - the exact
// shape of drift that goes unnoticed until a command behaves oddly during an
// incident.
//
// This is another gotcha turned into a rule: it was found by reading
// install-dependencies.sh while writing a rotation runbook, not by anything
// failing, which is precisely why it deserves a check rather than a note.

var (
	talosctlPin  = regexp.MustCompile(`(?m)^TALOSCTL_VERSION=v(\d+)\.(\d+)\.(\d+)`)
	talosVersion = regexp.MustCompile(`(?m)^\s*talos_version\s*=\s*"v(\d+)\.(\d+)\.(\d+)"`)
)

func TestTalosctlPinTracksTheClusterVersion(t *testing.T) {
	root := repoRoot(t)

	// The pin moved out of install-dependencies.sh and into versions.env when
	// the runner image began sharing it - the workstation and the runner have
	// to agree on the toolchain, so a version is written once. The contract is
	// unchanged: something still has to keep the client in step with the
	// cluster.
	script, err := os.ReadFile(filepath.Join(root, "scripts", "versions.env"))
	if err != nil {
		t.Fatalf("reading versions.env: %v", err)
	}
	vars, err := os.ReadFile(filepath.Join(root, "management", "cluster", "variables.tf"))
	if err != nil {
		t.Fatalf("reading variables.tf: %v", err)
	}

	cli := talosctlPin.FindSubmatch(script)
	if cli == nil {
		t.Fatal(`could not find TALOSCTL_VERSION=vX.Y.Z in scripts/versions.env.

If the pin was restructured, this contract needs re-examining rather than
re-pointing: something still has to keep the client in step with the cluster.`)
	}
	cluster := talosVersion.FindSubmatch(vars)
	if cluster == nil {
		t.Fatal("could not find talos_version = \"vX.Y.Z\" in variables.tf - see the note above")
	}

	cliMajor, cliMinor := atoi(t, cli[1]), atoi(t, cli[2])
	clMajor, clMinor := atoi(t, cluster[1]), atoi(t, cluster[2])

	if cliMajor != clMajor {
		t.Fatalf("talosctl is pinned to v%d.%d but the cluster runs v%d.%d - different major versions",
			cliMajor, cliMinor, clMajor, clMinor)
	}

	// Within one minor, in either direction. A client newer than the cluster
	// is the normal state during an upgrade; two minors apart is not.
	if diff := cliMinor - clMinor; diff > 1 || diff < -1 {
		t.Errorf(`talosctl is pinned to v%d.%d.%s but the cluster runs v%d.%d.%s.

Sidero supports a client within one minor of the cluster. Two apart is where
commands start behaving differently from their documentation - which is a bad
thing to discover while running `+"`talosctl rotate-ca`"+` during an incident.

  scripts/install-dependencies.sh   TALOSCTL_VERSION
  management/cluster/variables.tf   talos_version`,
			cliMajor, cliMinor, cli[3], clMajor, clMinor, cluster[3])
	}
}

func atoi(t *testing.T, b []byte) int {
	t.Helper()
	n, err := strconv.Atoi(string(b))
	if err != nil {
		t.Fatalf("%q is not a number: %v", b, err)
	}
	return n
}

// kubectl is a client for a specific Kubernetes version, and the project
// supports it within one minor of the cluster in either direction. Same shape
// as the talosctl contract above, same reason: the two numbers live in
// different files and drift without anything failing.
//
// It had drifted, in both directions at once. The cluster ran 1.31.1 pinned
// inline in talos.tf where nothing watched it, versions.env said 1.34.11, and
// the workstation installer ignored the pin and fetched whatever upstream
// called stable - three numbers, no two of them within a minor of each other.
var (
	kubectlPin        = regexp.MustCompile(`(?m)^KUBECTL_VERSION=v(\d+)\.(\d+)\.(\d+)`)
	kubernetesVersion = regexp.MustCompile(`(?m)^\s*kubernetes_version\s*=\s*"(\d+)\.(\d+)\.(\d+)"`)
)

func TestKubectlPinTracksTheClusterVersion(t *testing.T) {
	root := repoRoot(t)

	env, err := os.ReadFile(filepath.Join(root, "scripts", "versions.env"))
	if err != nil {
		t.Fatalf("reading versions.env: %v", err)
	}
	vars, err := os.ReadFile(filepath.Join(root, "management", "cluster", "variables.tf"))
	if err != nil {
		t.Fatalf("reading variables.tf: %v", err)
	}

	cli := kubectlPin.FindSubmatch(env)
	if cli == nil {
		t.Fatal(`could not find KUBECTL_VERSION=vX.Y.Z in scripts/versions.env.

If the pin was restructured, this contract needs re-examining rather than
re-pointing: something still has to keep the client in step with the cluster.`)
	}
	cluster := kubernetesVersion.FindSubmatch(vars)
	if cluster == nil {
		t.Fatal(`could not find kubernetes_version = "X.Y.Z" in management/cluster/variables.tf.

It was inline in talos.tf once, which is how it went five minors stale without
anything noticing. It belongs in the locals beside talos_version, because Talos
decides which Kubernetes versions are installable at all.`)
	}

	cliMajor, cliMinor := atoi(t, cli[1]), atoi(t, cli[2])
	clMajor, clMinor := atoi(t, cluster[1]), atoi(t, cluster[2])

	if cliMajor != clMajor {
		t.Fatalf("kubectl is pinned to v%d.%d but the cluster runs %d.%d - different major versions",
			cliMajor, cliMinor, clMajor, clMinor)
	}
	if diff := cliMinor - clMinor; diff > 1 || diff < -1 {
		t.Errorf(`kubectl is pinned to v%d.%d.%s but the cluster runs %d.%d.%s.

Kubernetes supports a client within one minor of the API server. Further apart
and commands start failing or behaving differently from their documentation,
which is a bad thing to discover while draining a node during an incident.`,
			cliMajor, cliMinor, cli[3], clMajor, clMinor, cluster[3])
	}
}

// The pin is only a pin if the thing installing it reads it.
//
// install-dependencies.sh sourced versions.env and then overwrote
// KUBECTL_VERSION with `curl https://dl.k8s.io/release/stable.txt`, so the
// workstation installed whatever upstream called stable that day while the
// runner image honoured the pin. The file whose entire job is making those two
// agree was the one being ignored, and the test above would have been checking
// a number nothing used. #178.
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
