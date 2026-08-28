package phases

import (
	"fmt"
	"io"
	"os"
	"strings"

	"homelab/ignite/internal/config"
	"homelab/ignite/internal/onepassword"
	"homelab/ignite/internal/run"
)

// CheckVault proves the vault holds everything the config template asks for,
// before a run commits to anything.
//
// This is AssertRenderedConfigComplete moved as far left as it can go. That
// check compares the template against an already-rendered config, so it can
// only speak after Render has pulled every secret in the estate onto disk -
// it is correct, and it is the most expensive possible moment to learn that
// one field was misspelled. This asks the same question with nothing written:
// no rendered config, no secrets on disk, nothing to sterilize afterwards.
//
// It is not a phase. It creates nothing, waits for nothing, and belongs in the
// ignition sequence no more than -kubeconfig does; it is the thing you run
// when you have just edited the template or the vault and want to know whether
// the two still agree.
//
// It reports structure only. Each reference comes back as ok / empty /
// missing - never a value - so the output is safe to paste into an issue or a
// pull request, which is exactly when somebody most wants to share it.
func CheckVault(ctx *run.Context) error {
	run.WritePhase("Check Vault", "Prove every op:// reference resolves, without reading a value.")

	if !onepassword.Available() {
		return fmt.Errorf("the 1Password CLI (op) is not on PATH. Install it with ./scripts/install-dependencies.sh")
	}
	if !onepassword.SignedIn() {
		return fmt.Errorf("not signed in to 1Password. Run `op signin` first")
	}

	refs, err := config.VaultReferences(ctx.ConfigTpl)
	if err != nil {
		return err
	}
	return vaultReport(refs, onepassword.Probe, os.Stdout)
}

// vaultReport probes every reference and writes the result.
//
// The probe is injected rather than called directly so the reporting - which
// is where a mistake would actually live - is testable with no vault, no
// credentials and no op binary at all.
func vaultReport(refs []config.VaultRef, probe func(string) onepassword.Status, out io.Writer) error {
	// A template with no references means this is reading the wrong file, and
	// reporting success would be worse than useless: it would be an all-clear
	// that could never have been anything else.
	if len(refs) == 0 {
		return fmt.Errorf("the config template declares no op:// references at all. That is not a complete vault, it is the wrong file")
	}

	width := 0
	for _, r := range refs {
		if len(r.ConfigPath) > width {
			width = len(r.ConfigPath)
		}
	}

	var missing, empty []config.VaultRef
	for _, r := range refs {
		status := probe(r.Ref)
		switch status {
		case onepassword.StatusMissing:
			missing = append(missing, r)
		case onepassword.StatusEmpty:
			empty = append(empty, r)
		}
		fmt.Fprintf(out, "  %-7s %-*s  %s\n", status, width, r.ConfigPath, r.Ref)
	}
	fmt.Fprintf(out, "\n  %d checked, %d missing, %d empty\n", len(refs), len(missing), len(empty))

	// Missing first: it is the harder failure and the one that stops a run
	// dead at Render, where an empty field sails through and surfaces much
	// later inside a provider.
	var problems []string
	if len(missing) > 0 {
		problems = append(problems, fmt.Sprintf("%d reference(s) do not resolve:\n\n%s\n\nThe item or field does not exist, or the path is misspelled. Create it, or\nremove the entry from config/management.tpl.json if this estate does not\nneed it - a reference to a field that does not exist fails every run at the\nRender phase.", len(missing), listRefs(missing)))
	}
	if len(empty) > 0 {
		problems = append(problems, fmt.Sprintf("%d field(s) exist but are empty:\n\n%s\n\nop inject treats a blank field as success and writes an empty string, so\nthis does not fail Render - it reaches a provider as something like\n\"credentials are empty\", naming no field. Fill them in.", len(empty), listRefs(empty)))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n\n"))
	}

	run.Ok(fmt.Sprintf("all %d vault references resolve", len(refs)))
	return nil
}

func listRefs(refs []config.VaultRef) string {
	lines := make([]string, 0, len(refs))
	for _, r := range refs {
		lines = append(lines, fmt.Sprintf("  %s  <-  %s", r.ConfigPath, r.Ref))
	}
	return strings.Join(lines, "\n")
}
