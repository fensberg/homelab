package phases

import (
	"fmt"
	"strings"

	"homelab/contractor/internal/run"
	"homelab/details/onepassword"
	"homelab/details/secrets"
	"homelab/details/vaults"
)

// The estate's break-glass keypair, addressed once so that the Render, Backup
// and Sterilize phases cannot drift apart on where it lives. There is no site
// in these paths on purpose: one key covers the whole estate.
//
// The two halves live in different vaults, and that is the point. The
// recipient is public, so it is in estate-shared, which every site reads. The
// identity is in the estate's own vault, which no site's token can see at all,
// so this program cannot read it even by mistake: a restore takes it piped on
// stdin from a person holding the estate token. BackupIdentityRef exists only
// so the program can tell that person where it is, and no OpenTofu file may
// name it (tests/go/repo/breakglass_test.go).
var (
	BackupRecipientRef = "op://" + vaults.EstateShared + "/state_backup/recipient"
	BackupIdentityRef  = "op://" + vaults.Estate + "/state_backup/identity"
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
	if err := ensureWorldBackupKey(ctx.Site); err != nil {
		return err
	}
	return assertBackupKeypair(ctx)
}

// WorldBackupKeyRef encrypts the game world's backups. The site's, like the
// world it protects.
func WorldBackupKeyRef(site string) string { return "op://" + site + "/valheim/backup_key" }

// ensureWorldBackupKey generates the key the game server's backups are
// encrypted with.
//
// By this file's rule: management/cluster/workloads.tf writes it into a
// Secret, so it reaches state, so it is ours to generate. Nobody types it -
// the backup sidecar encrypts with it and the restore init container
// decrypts with it, both from that Secret, so a rebuilt estate restores the
// world with no human in the loop. A backup bucket that leaks without this
// key yields ciphertext.
func ensureWorldBackupKey(site string) error {
	ref, err := onepassword.ParseRef(WorldBackupKeyRef(site))
	if err != nil {
		return err
	}
	_, status, err := onepassword.EnsureField(ref, func() (string, error) {
		return secrets.Password(44)
	})
	if err != nil {
		return fmt.Errorf("world backup key: %w", err)
	}
	if status == "generated" {
		run.Ok("generated a world backup key and stored it in 1Password")
	}
	return nil
}

func ensureStatePassword(ctx *run.Context) error {
	ref, err := onepassword.ParseRef(fmt.Sprintf("op://%s/database/password", ctx.Site))
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
// because it must not come from here.
//
// It lives outside every site, because there is one break-glass key for the
// whole estate. That is not a convenience: the recipient is a public key, so
// sharing it across sites costs nothing, while rotating it strands every
// backup already encrypted to the previous one. A key per site would multiply
// the number of private halves a restore has to find, for no security gained.
//
// Only the recipient is checked. The identity is in the estate's own vault,
// which this site's token cannot see - so its absence cannot be told from its
// presence here, and that is the property rather than a gap: a site that
// could confirm the private half exists could read it.
func assertBackupKeypair(*run.Context) error {
	recipientRef, err := onepassword.ParseRef(BackupRecipientRef)
	if err != nil {
		return err
	}
	identityRef, err := onepassword.ParseRef(BackupIdentityRef)
	if err != nil {
		return err
	}
	if v, err := onepassword.Read(recipientRef.String()); err == nil && strings.TrimSpace(v) != "" {
		return nil
	}
	return fmt.Errorf(`the estate has no state-backup recipient at %s, and ignite will not create one.

It is the break-glass: its whole purpose is to be outside this program's
control, so generating it here would defeat it. With the estate's token, make
one once for the estate and store the two halves in their two vaults - the
public recipient where every site reads it, the private identity where no site
can:

    age-keygen -o backup.key            # prints the age1... recipient
    op item edit %s --vault %s %s[text]=<the age1 line>
    op item edit %s --vault %s %s[password]=<the AGE-SECRET-KEY line>
    shred -u backup.key

The same keypair serves every site - one break-glass key, not one per site`,
		recipientRef, recipientRef.Item, recipientRef.Vault, fieldAddress(recipientRef),
		identityRef.Item, identityRef.Vault, fieldAddress(identityRef))
}

// fieldAddress renders the "section.field" (or bare "field") form that
// `op item edit` assigns to. The estate keypair sits directly on its item, so
// the section half is genuinely absent rather than merely unknown, and an
// instruction that printed a leading dot would not work when pasted.
func fieldAddress(r onepassword.Ref) string {
	if r.Section == "" {
		return r.Field
	}
	return r.Section + "." + r.Field
}
