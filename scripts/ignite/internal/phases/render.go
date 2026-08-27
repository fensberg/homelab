package phases

import (
	"fmt"
	"os"
	"strings"

	"homelab/ignite/internal/config"
	"homelab/ignite/internal/onepassword"
	"homelab/ignite/internal/run"
)

// Render pulls secrets out of 1Password into gitignored files.
func Render(ctx *run.Context) error {
	run.WritePhase("Render", "Pull secrets from 1Password into gitignored files.")

	if err := EnsureVaultSession(); err != nil {
		return err
	}

	// Before the inject, not after: the template resolves the site's
	// database password and the estate's backup recipient, so anything this
	// generates has to be in the vault by the time op reads it.
	if err := ensureGeneratedSecrets(ctx); err != nil {
		return err
	}

	run.Info("rendering config/management.rendered.json")
	if err := onepassword.Inject(ctx.ConfigTpl, ctx.ConfigRendered); err != nil {
		return err
	}

	// The inventory is generated, not templated: op inject substitutes into
	// a fixed file and cannot loop over sites[].hypervisor.nodes. Generating
	// it is what makes appending a node genuinely sufficient.
	cfg, err := config.LoadRendered(ctx.ConfigRendered)
	if err != nil {
		return err
	}
	net, err := config.ResolveSiteNetwork(cfg, ctx.Site)
	if err != nil {
		return err
	}

	run.Info(fmt.Sprintf("generating inventory for %s (%d hypervisor(s))", net.Name, len(net.Hypervisors)))
	var inv strings.Builder
	inv.WriteString("---\nall:\n  children:\n    hypervisors:\n      hosts:\n")
	for _, h := range net.Hypervisors {
		fmt.Fprintf(&inv, "        %q:\n", h.Hostname)
		fmt.Fprintf(&inv, "          ansible_host: %q\n", h.IP)
		inv.WriteString("          ansible_user: root\n")
	}
	if err := os.WriteFile(ctx.InventoryOut, []byte(inv.String()), 0o644); err != nil {
		return err
	}

	// Not a secret, but it lives beside the rendered files and is wiped
	// with them, so the playbook only ever sees one site's values.
	run.Info("writing per-site network values for Ansible")
	siteVars := fmt.Sprintf(`---
sdn_subnet: %q
sdn_gateway: %q
advertise_routes: %q
sdn_asn: %d
sdn_vrf_vni: %d
sdn_vnet_vni: %d
`, net.NodeCIDR, net.Gateway, net.SiteCIDR, net.ASN, net.VRFVNI, net.VNetVNI)
	if err := os.WriteFile(ctx.SiteVars, []byte(siteVars), 0o644); err != nil {
		return err
	}

	if err := config.AssertRenderedConfigComplete(ctx.ConfigTpl, ctx.ConfigRendered); err != nil {
		return err
	}
	run.Ok("secrets rendered")
	return nil
}

// EnsureVaultSession makes sure `op` is present and signed in.
//
// Signing in here rather than failing and making the operator do it: an
// unsigned CLI otherwise surfaces as `op inject` emitting a half-rendered
// file, which is a far worse failure than a prompt. Shared with
// EnsureStateEncryption, which needs a session before the first phase runs and
// therefore before Render has had a chance to establish one.
func EnsureVaultSession() error {
	if !onepassword.Available() {
		return fmt.Errorf("1Password CLI ('op') not found on PATH")
	}
	if !onepassword.SignedIn() {
		run.Info("not signed in to 1Password - starting sign-in")
		_ = onepassword.SignIn()
		if !onepassword.SignedIn() {
			return fmt.Errorf(`still not signed in to 1Password after attempting sign-in.

If the desktop app is installed, enable Settings > Developer > Integrate with
1Password CLI, unlock the app, and re-run. Otherwise sign in manually:

    op signin`)
		}
	}
	if email, err := onepassword.WhoamiEmail(); err == nil && email != "" {
		run.Ok("signed in to 1Password as " + email)
	}
	return nil
}
