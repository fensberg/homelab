package repo

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every listener the machine configuration opens is dialled by the integration
// tier, and every listener the tier dials is one the configuration opens.
//
// WHAT THIS GUARDS. management/cluster/talos.tf declared
// `listen-metrics-urls = "http://0.0.0.0:2381"` on etcd and the estate did not
// have it. The declaration sat in the repository, correct and inert, while
// Prometheus had no etcd target and Alertmanager paged every four hours
// claiming etcd had lost quorum (#493).
//
// Nothing could have caught it. `tofu plan` cannot: the provider's
// talos_machine_configuration_apply records the configuration it SENT, so a
// machine that accepted a change and never acted on it is identical in state
// to one that did. The repo tier proved the declaration was present. The
// deployed tier proved Prometheus was unhappy. Neither owned the question in
// between - is the machine running what we declared - and so the only way to
// answer it was a person with a terminal and talosctl.
//
// The answer is a test that dials the port
// (TestEveryControlPlaneOpensTheListenersItWasConfiguredFor). This is what
// stops that test going stale: a listener added to talos.tf and not added
// there would be a new declaration nothing confirms, which is the state that
// produced this in the first place.
//
// Deliberately matched on the component rather than the port. The port is a
// component default and is not written in talos.tf, so it lives in the
// integration table with a comment; what talos.tf does say is which component
// is being told to listen somewhere other than loopback, and that is the fact
// both sides have to agree about.
func TestEveryDeclaredControlPlaneListenerIsDialled(t *testing.T) {
	root := repoRoot(t)

	declared := declaredListeners(t, readFile(t, filepath.Join(root, "management", "cluster", "talos.tf")))
	if len(declared) == 0 {
		t.Fatal(`talos.tf declares no control-plane listener.

Either the cluster.<component>.extraArgs patch was removed - in which case the
control plane is unscrapeable again and the integration tier should be failing -
or it was restructured and this guard is now reading the wrong shape. Both need
a person; neither is fixed by deleting this test.`)
	}

	dialled := dialledListeners(t, readFile(t, filepath.Join(root, "tests", "go", "integration", "control_plane_listeners_test.go")))

	for _, component := range sortedKeys(declared) {
		if !dialled[component] {
			t.Errorf(`talos.tf tells cluster.%s to listen on %s, and nothing dials it.

A declared listener nobody confirms is exactly how etcd's metrics port sat
unopened while the repository said otherwise. Add an entry to
controlPlaneListeners in tests/go/integration/control_plane_listeners_test.go
with the port that component serves on.`, component, declared[component])
		}
	}

	for _, component := range sortedKeys(dialled) {
		if _, ok := declared[component]; !ok {
			t.Errorf(`the integration tier dials cluster.%s, and talos.tf does not configure it to listen.

The test would be asserting a listener nothing asks for: either passing because
a default happens to open it, or failing about a promise the repository never
made. Remove the entry, or restore the machine-config patch that opened it.`, component)
		}
	}
}

// declaredListeners finds each cluster component told to listen somewhere
// other than loopback, mapped to the argument that says so.
//
// Read out of the yamlencode patch rather than from a list here, so a
// component added to talos.tf is discovered rather than remembered.
var (
	extraArgsBlock = regexp.MustCompile(`(?s)(\w+) = \{\s*\n\s*extraArgs = \{(.*?)\n\s*\}`)
	extraArg       = regexp.MustCompile(`(?m)^\s*([\w-]+)\s*=\s*"([^"]*)"`)
)

func declaredListeners(t *testing.T, body string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, block := range extraArgsBlock.FindAllStringSubmatch(body, -1) {
		component := block[1]
		for _, arg := range extraArg.FindAllStringSubmatch(block[2], -1) {
			name, value := arg[1], arg[2]
			if !opensAListener(name, value) {
				continue
			}
			out[component] = name + " = " + value
		}
	}
	return out
}

// opensAListener reports whether an extraArg puts a service on an address
// something else could reach. An argument bound to loopback is the Talos
// default being restated and opens nothing.
func opensAListener(name, value string) bool {
	isAddress := name == "bind-address" || strings.Contains(name, "listen")
	if !isAddress {
		return false
	}
	return !strings.Contains(value, "127.0.0.1") && !strings.Contains(value, "localhost")
}

var dialledComponent = regexp.MustCompile(`(?m)^\s*Component:\s*"(\w+)",`)

func dialledListeners(t *testing.T, body string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, m := range dialledComponent.FindAllStringSubmatch(body, -1) {
		out[m[1]] = true
	}
	if len(out) == 0 {
		t.Fatal("found no Component entries in control_plane_listeners_test.go, so this guard is reading the wrong shape and would pass whatever talos.tf said")
	}
	return out
}
