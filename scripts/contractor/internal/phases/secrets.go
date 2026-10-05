package phases

import (
	"fmt"
	"strings"
	"time"

	"homelab/contractor/config"
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
	if err := ensureApplicationSecrets(ctx, onepassword.EnsureField); err != nil {
		return err
	}
	if err := ensureHostMetrics(ctx, onepassword.Probe, onepassword.WriteItem, time.Now()); err != nil {
		return err
	}
	return assertBackupKeypair(ctx)
}

// The host's exporter and the one scraper it admits.
//
// The exporter on a hypervisor serves over TLS and asks whoever connects for
// a certificate (docs/epochs/04-observability.md). Both sides' certificates
// and the authority that signed them are generated here, by this file's rule:
// the scraper's key is written into a Secret, so it reaches state, so it is
// ours to generate. They are an item of their own in the site's vault, which
// the hypervisor playbook reads the exporter's half from and the platform
// reads the scraper's half from.
const (
	hostMetricsItem = "host_metrics"
	// HostMetricsName is what the exporter answers as: the name the cluster
	// reaches it by, and so the name in its certificate.
	HostMetricsName = "hypervisor.monitoring.svc"
)

// ensureHostMetrics generates the host's scrape credentials when the site has
// none, and refuses a set that is partly there.
//
// All or nothing, because the five are one thing: two certificates signed by
// an authority whose key was dropped when it had signed them. A missing field
// cannot be made again to match the rest, and generating the rest again
// beside it would leave a hypervisor and a cluster holding halves of
// different sets.
func ensureHostMetrics(
	ctx *run.Context,
	probe func(ref string) onepassword.Status,
	write func(vault, title string, fields map[string]string) ([]string, error),
	now time.Time,
) error {
	fields := []string{"authority", "certificate", "private_key", "scraper_certificate", "scraper_private_key"}
	var missing []string
	for _, f := range fields {
		if probe(fmt.Sprintf("op://%s/%s/%s", ctx.Site, hostMetricsItem, f)) != onepassword.StatusOK {
			missing = append(missing, f)
		}
	}
	switch len(missing) {
	case 0:
		return nil
	case len(fields):
	default:
		return fmt.Errorf(`the host's scrape credentials are incomplete: %s/%s has no %s.

They are generated together and one cannot be replaced alone. Delete the item
and run this again: a new set is generated, the hypervisor phase installs the
exporter's half and a converge the scraper's`, ctx.Site, hostMetricsItem, strings.Join(missing, ", "))
	}

	c, err := secrets.ScrapeTLS(HostMetricsName, now)
	if err != nil {
		return fmt.Errorf("the host's scrape credentials: %w", err)
	}
	// Trimmed: a field is read back without the newline a PEM block ends in.
	if _, err := write(ctx.Site, hostMetricsItem, map[string]string{
		"authority":           strings.TrimSpace(c.Authority),
		"certificate":         strings.TrimSpace(c.Certificate),
		"private_key":         strings.TrimSpace(c.PrivateKey),
		"scraper_certificate": strings.TrimSpace(c.ScraperCertificate),
		"scraper_private_key": strings.TrimSpace(c.ScraperPrivateKey),
	}); err != nil {
		return fmt.Errorf("the host's scrape credentials: %w", err)
	}
	run.Ok("generated the host's scrape credentials and stored them in 1Password")
	return nil
}

// ensureApplicationSecrets generates what the site's applications declared
// as generated: a field of an application's vault item that nobody types.
//
// By this file's rule: the platform writes each into a Secret, so it reaches
// state, so it is ours to generate. An application says which of its fields
// those are (generated, in its declaration); this knows no application by
// name. A key that encrypts backups is the usual case: the application
// encrypts and decrypts with it from that Secret, so a rebuilt site restores
// with no human in the loop, and a bucket that leaks without it yields
// ciphertext.
//
// Written to the site's own vault, like everything the contractor generates
// for a site - never a -shared one, which the lawyer writes and other readers
// trust, and never the estate's, which no site can see.
func ensureApplicationSecrets(ctx *run.Context, ensure func(ref onepassword.Ref, generate func() (string, error)) (string, string, error)) error {
	declared, assigned, err := config.SiteApplications(ctx.RepoRoot, ctx.Site)
	if err != nil {
		return err
	}
	for _, as := range assigned {
		for _, field := range declared[as.Application].GeneratedFields() {
			ref, err := onepassword.ParseRef(config.ApplicationRef(ctx.Site, as.Application, field))
			if err != nil {
				return err
			}
			_, status, err := ensure(ref, func() (string, error) { return secrets.Password(generatedSecretLength) })
			if err != nil {
				return fmt.Errorf("%s's %s: %w", as.Application, field, err)
			}
			if status == "generated" {
				run.Ok(fmt.Sprintf("generated %s's %s and stored it in 1Password", as.Application, field))
			}
		}
	}
	return nil
}

// generatedSecretLength is long enough to be a key and short enough for
// anything that takes one as a passphrase.
const generatedSecretLength = 44

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
