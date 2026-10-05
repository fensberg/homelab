package phases

import (
	"fmt"
	"io"
	"os"
	"strings"

	"homelab/contractor/config"
	"homelab/contractor/internal/run"
	"homelab/details/onepassword"
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

	var missing, empty, unsafe []config.VaultRef
	for _, r := range refs {
		status := probe(r.Ref)
		switch status {
		case onepassword.StatusMissing:
			missing = append(missing, r)
		case onepassword.StatusEmpty:
			empty = append(empty, r)
		case onepassword.StatusBreaksConfig:
			unsafe = append(unsafe, r)
		}
		fmt.Fprintf(out, "  %-7s %-*s  %s\n", status, width, r.ConfigPath, r.Ref)
	}
	fmt.Fprintf(out, "\n  %d checked, %d missing, %d empty, %d unsafe\n", len(refs), len(missing), len(empty), len(unsafe))

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
	if len(unsafe) > 0 {
		problems = append(problems, breaksConfig(unsafe))
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

// breaksConfig says which fields hold a value the config cannot carry.
func breaksConfig(unsafe []config.VaultRef) string {
	return fmt.Sprintf(`%d field(s) hold a line break, a quote or a backslash:

%s

The config is a JSON template and a value goes into it exactly as it is, so
one of these makes the rendered config unparsable and stops every verb that
renders. Keep a value of more than one line base64-encoded on a single line,
as the runner's private key is.`, len(unsafe), listRefs(unsafe))
}

// whichValuesBreakTheConfig is asked when the rendered config did not parse:
// it reads every reference again and names the ones that could be why.
//
// Only then, because it is a read of the vault per reference. The parser's
// own error is a byte offset into a file that is about to be wiped, and the
// first time this happened it took reading the change that caused it to find
// the field.
func whichValuesBreakTheConfig(template string, probe func(string) onepassword.Status) string {
	refs, err := config.VaultReferences(template)
	if err != nil {
		return ""
	}
	var unsafe []config.VaultRef
	for _, r := range refs {
		if probe(r.Ref) == onepassword.StatusBreaksConfig {
			unsafe = append(unsafe, r)
		}
	}
	if len(unsafe) == 0 {
		return ""
	}
	return breaksConfig(unsafe)
}
