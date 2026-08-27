package phases

import (
	"fmt"
	"strings"

	"homelab/ignite/internal/onepassword"
	"homelab/ignite/internal/run"
	"homelab/ignite/internal/secrets"
)

// ensureGeneratedSecrets creates the credentials this project owns end to end,
// before the config that reads them is rendered.
//
// Which secrets belong here is not a judgement call - it falls out of where
// they end up. A secret that becomes a resource attribute is written into
// OpenTofu state, so a leaked state file yields a live credential; those are
// ours to generate and, eventually, to rotate. A secret that only configures a
// provider never reaches state at all: the Proxmox token, the Tailscale OAuth
// secret and the Cloudflare admin token are used to build and are gone, so
// they stay a human's to manage and are the "honest floor" this project
// already documents.
//
// The practical payoff is that a brand new site needs a human to supply the
// credentials that genuinely come from a console, and nothing else. Nobody has
// to invent a database password that nobody will ever read.
func ensureGeneratedSecrets(ctx *run.Context) error {
	run.Info("checking the secrets this project generates for itself")

	if err := ensureStatePassword(ctx); err != nil {
		return err
	}
	return assertBackupKeypair(ctx)
}

func ensureStatePassword(ctx *run.Context) error {
	ref, err := onepassword.ParseRef(fmt.Sprintf("op://homelab/%s/state_database/db_password", ctx.Site))
	if err != nil {
		return err
	}
	_, status, err := onepassword.EnsureField(ref, func() (string, error) {
		return secrets.Password(32)
	})
	if err != nil {
		return fmt.Errorf("state database password: %w", err)
	}
	if status == "generated" {
		run.Ok("generated a state database password and stored it in 1Password")
	}
	return nil
}

// assertBackupKeypair checks that the estate's age keypair is present, and
// deliberately does NOT create one.
//
// This is the one credential that must stay outside the automation's control,
// because its entire job is to be the thing that survives the automation being
// compromised. Generating it here would mean ignite holding the private half
// at creation time and writing it into a vault ignite can read - which
// forecloses the property the key exists to provide, in the same breath as
// creating it.
//
// So it joins the honest floor already documented in CLAUDE.md: the source
// control token, the overlay OAuth client and the object storage tokens are
// human-supplied because they come from a console. This one is human-supplied
// because it must not come from here. It is generated once for the estate
// with `age-keygen`, not once per site - one break-glass key, stored once,
// rotated deliberately and rarely, because rotating it strands every backup
// encrypted to the previous recipient.
func assertBackupKeypair(ctx *run.Context) error {
	recipientRef, err := onepassword.ParseRef(fmt.Sprintf("op://homelab/%s/state_database/backup_recipient", ctx.Site))
	if err != nil {
		return err
	}
	identityRef, err := onepassword.ParseRef(fmt.Sprintf("op://homelab/%s/state_database/backup_identity", ctx.Site))
	if err != nil {
		return err
	}

	present := func(r onepassword.Ref) bool {
		v, err := onepassword.Read(r.String())
		return err == nil && strings.TrimSpace(v) != ""
	}
	haveRecipient, haveIdentity := present(recipientRef), present(identityRef)

	switch {
	case haveRecipient && haveIdentity:
		return nil

	case !haveRecipient && !haveIdentity:
		return fmt.Errorf(`this site has no backup keypair, and ignite will not create one.

It is the break-glass: its whole purpose is to be outside this program's
control, so generating it here would defeat it. Make one once for the estate
and store both halves:

    age-keygen -o backup.key            # prints the age1... recipient
    op item edit %s --vault %s \
      %s.backup_recipient[text]=<the age1 line> \
      %s.backup_identity[password]=<the AGE-SECRET-KEY line>
    shred -u backup.key

The same keypair serves every site - one break-glass key, not one per site`,
			recipientRef.Item, recipientRef.Vault, recipientRef.Section, identityRef.Section)

	case haveRecipient && !haveIdentity:
		// Backups are being taken and encrypted to a key whose private half
		// this project cannot see. Not fatal - the backups are still valid -
		// but a restore depends on finding a file rather than opening a vault.
		run.Warn("backup_recipient is set but backup_identity is not.")
		run.Warn("Backups are encrypted to a key whose private half is not in the vault, so a")
		run.Warn("restore depends on someone finding it. Store it beside the recipient:")
		run.Warn("    op item edit " + identityRef.Item + " --vault " + identityRef.Vault +
			" " + identityRef.Section + "." + identityRef.Field + "[password]=<the AGE-SECRET-KEY line>")
		return nil

	default: // identity without recipient
		return fmt.Errorf(`this site has a backup_identity but no backup_recipient.

The Backup phase has nothing to encrypt to. Derive the recipient from the
identity you already hold rather than making a new pair, which would strand
whatever the stored identity was for:

    op read "%s" | age-keygen -y`, identityRef)
	}
}
