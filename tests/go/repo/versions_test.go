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

	script, err := os.ReadFile(filepath.Join(root, "scripts", "install-dependencies.sh"))
	if err != nil {
		t.Fatalf("reading install-dependencies.sh: %v", err)
	}
	vars, err := os.ReadFile(filepath.Join(root, "management", "cluster", "variables.tf"))
	if err != nil {
		t.Fatalf("reading variables.tf: %v", err)
	}

	cli := talosctlPin.FindSubmatch(script)
	if cli == nil {
		t.Fatal(`could not find TALOSCTL_VERSION=vX.Y.Z in install-dependencies.sh.

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
