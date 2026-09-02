package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A workstation that is not on the overlay cannot reach the estate.
//
// The hypervisor is addressed by its overlay identity, because that is the only
// address that works from inside the cluster as well as outside it (#99). A
// devbox on the LAN alone hands that address to the LAN gateway, which
// black-holes it - and a provider given an unreachable API does not fail, it
// waits. Every local `break-ground` and `demolish` then hangs with no error at
// all. That cost a full day before anybody read a route table.
//
// So the provisioning has to put the machine on the overlay, and has to refuse
// to build one it cannot. Both halves are checked: the second is what stops an
// empty auth key producing a workstation that comes up looking healthy and
// reaches nothing, since `tailscale up --auth-key=”` exits zero having joined
// nothing.
func TestTheWorkstationIsProvisionedOntoTheOverlay(t *testing.T) {
	body := readWorkstationPlaybook(t)

	if !strings.Contains(body, "tailscale up") {
		t.Error("workstation/provision.yml never joins the overlay.\n\n" +
			"The hypervisor is addressed by its overlay identity, so a workstation " +
			"without it cannot reach the machine it is meant to manage - and the " +
			"failure is a hang rather than an error.")
	}
	if !strings.Contains(body, "pkgs.tailscale.com") {
		t.Error("the overlay client is not installed from its signed apt repository.\n\n" +
			"`curl | sh` is the shape scripts/approved-suppliers.yml exists to prevent: " +
			"the package is signed, the install script is merely fetched.")
	}
	if !strings.Contains(body, "--accept-routes") {
		t.Error("the workstation does not accept advertised routes, so it loses the node " +
			"subnet whenever the SDN is rebuilt - which is precisely when somebody is " +
			"running the button and needs to watch it")
	}
}

// The router tag approves subnet routes. A workstation wearing it could claim
// a site's whole range.
func TestTheWorkstationDoesNotWearTheRouterTag(t *testing.T) {
	body := readWorkstationPlaybook(t)
	if strings.Contains(body, "tag:homelab-router") {
		t.Error("workstation/provision.yml uses the router tag.\n\n" +
			"autoApprovers trusts that tag to advertise subnet routes, so a workstation " +
			"carrying it can claim 10.0.0.0/8 and take over a site's addressing.")
	}
}

// An empty key must stop the build, not produce a machine that cannot reach
// anything. Fail closed: "no key supplied" and "joined successfully" must not
// look the same afterwards.
func TestAWorkstationWithNoOverlayKeyIsRefused(t *testing.T) {
	body := readWorkstationPlaybook(t)
	if !strings.Contains(body, "overlay_auth_key | length > 0") {
		t.Error("nothing refuses an empty overlay_auth_key.\n\n" +
			"`tailscale up --auth-key=''` exits zero having joined nothing, so the " +
			"workstation comes up looking healthy and reaches nothing - which is the " +
			"defect this provisioning exists to prevent, reintroduced by an empty string.")
	}
}

// Whatever tag the playbook advertises has to be one the tailnet policy lets
// somebody apply, or `tailscale up` is refused at first boot on a machine
// nobody is watching.
func TestTheWorkstationTagIsDocumentedInThePolicy(t *testing.T) {
	body := readWorkstationPlaybook(t)
	policy, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "tailnet-setup.md"))
	if err != nil {
		t.Fatalf("reading the tailnet policy documentation: %v", err)
	}
	const tag = "tag:homelab-workstation"
	if strings.Contains(body, tag) && !strings.Contains(string(policy), tag) {
		t.Errorf("the playbook advertises %s and docs/tailnet-setup.md never mentions it.\n\n"+
			"A tag nobody is permitted to apply makes `tailscale up` fail at first boot, "+
			"on a machine nobody is watching.", tag)
	}
}

func readWorkstationPlaybook(t *testing.T) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), "workstation", "provision.yml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(body)
}
