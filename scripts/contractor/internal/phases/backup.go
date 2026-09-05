package phases

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"homelab/contractor/internal/config"
	"homelab/contractor/internal/run"
)

// Backup encrypts the state and pushes it off-site to Cloudflare R2.
func Backup(ctx *run.Context) error {
	run.WritePhase("Backup", "Encrypt the state and push it off-site to Cloudflare R2.")

	cfg, err := config.LoadRendered(ctx.ConfigRendered)
	if err != nil {
		return err
	}
	site, ok := cfg.Sites[ctx.Site]
	if !ok {
		return fmt.Errorf("unknown site '%s'", ctx.Site)
	}
	store := site.ObjectStorage

	if strings.TrimSpace(cfg.ObjectStorage.AccountID) == "" {
		return fmt.Errorf("object_storage.account_id is empty in the rendered config")
	}

	for field, val := range map[string]string{
		"access_key_id":     store.AccessKeyID,
		"secret_access_key": store.SecretAccessKey,
		"bucket":            store.Bucket,
	} {
		if strings.TrimSpace(val) == "" {
			return fmt.Errorf("sites.%s.object_storage.%s is missing from the rendered config", ctx.Site, field)
		}
	}
	recipient := strings.TrimSpace(cfg.StateBackup.Recipient)
	if recipient == "" {
		return fmt.Errorf(`no 'state_backup.recipient' in the rendered config.

State is never uploaded in plaintext. Generate a key pair once for the whole
estate - not one per site:

    age-keygen -o state-backup.key

Put the PUBLIC recipient (the 'age1...' line) in 1Password at
%s, and the private half beside it at %s. The
automation only ever needs the public half - it can write backups but cannot
read them back.`, BackupRecipientRef, BackupIdentityRef)
	}

	for _, tool := range []string{"age", "rclone"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("'%s' not found on PATH", tool)
		}
	}

	stamp := time.Now().Format("20060102-150405")
	// Only the ciphertext ever becomes a file. The plaintext is held in
	// memory and piped straight into age.
	//
	// Created here rather than named here, and the difference is the point.
	// A path built from the timestamp is predictable, and it lands in a
	// directory shared with every other account on the machine - including
	// the unprivileged one this estate deliberately confines an agent to.
	// Worse, age creates its --output file honouring the caller's umask, so
	// under either common default (0022 or 0002) the encrypted state of the
	// entire estate arrived world-readable. Ciphertext is not an excuse: handing any
	// account an offline copy of the estate's state is precisely what the
	// privilege boundary exists to prevent, and it only has to hold until
	// the recipient half leaks once. os.CreateTemp gives an unpredictable
	// name at 0600, which is what the two other temp files in this program
	// (the kubeconfig in health.go, the identity in restore.go) already do.
	tmp, err := os.CreateTemp("", fmt.Sprintf("tofu-state-%s-*.json.age", stamp))
	if err != nil {
		return fmt.Errorf("creating the temporary ciphertext file: %w", err)
	}
	tmpCipher := tmp.Name()
	defer os.Remove(tmpCipher)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("securing the temporary ciphertext file: %w", err)
	}
	// Closed, not written through: age opens the path itself. Truncating an
	// existing file leaves its mode alone, so the 0600 set here survives.
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing the temporary ciphertext file: %w", err)
	}

	// Pull from whichever backend is currently authoritative. After the
	// Migrate phase that is Postgres, which is exactly what we want to back
	// up - the local file is already gone by then.
	//
	// The plaintext never becomes a file. It used to: state was written to
	// /tmp, age-encrypted from there, and both removed in a defer. That is
	// the weaker shape, and it is the one this project's own rule argues
	// against - deleting a file assumes the delete happens, that nothing
	// copied it first, and that no crash dump or snapshot caught it in
	// between. Piping it into age's stdin makes all three assumptions
	// unnecessary. The buffer is wiped as soon as age has consumed it.
	// The workspace has to be able to reach the state before it can pull it.
	//
	// `task backup-state` and the nightly tier both run this phase on its own,
	// on a workspace that has never been initialised - so `tofu state pull`
	// failed with "Required plugins are not installed", naming all five
	// providers. A phase that is documented as runnable on its own has to
	// establish its own preconditions, which is the same rule the sterilize
	// exemption landed on from the other direction.
	if err := attachIfDetached(ctx); err != nil {
		return err
	}

	run.Info("pulling the current state")
	state, err := run.CmdOutputBytes(ctx.ClusterDir, "tofu", "state", "pull")
	defer run.Wipe(state)
	if err != nil {
		return fmt.Errorf("tofu state pull: %w", err)
	}
	if len(state) < 100 {
		return fmt.Errorf("state pull returned only %d bytes - refusing to upload it", len(state))
	}

	recipientPreview := recipient
	if len(recipientPreview) > 20 {
		recipientPreview = recipientPreview[:20]
	}
	run.Info("encrypting to " + recipientPreview + "...")
	if err := run.CmdStdin(ctx.ClusterDir, state, "age",
		"--recipient", recipient, "--output", tmpCipher); err != nil {
		return fmt.Errorf("age encrypt: %w", err)
	}
	run.Wipe(state)

	rcloneEnv := r2Env(cfg.ObjectStorage, store)

	dest := fmt.Sprintf("R2:%s/management-cluster", store.Bucket)
	run.Info(fmt.Sprintf("uploading to %s/%s.tfstate.age", dest, stamp))
	if err := run.CmdEnv(ctx.ClusterDir, rcloneEnv, "rclone", "--log-level", "ERROR", "copyto", tmpCipher, fmt.Sprintf("%s/%s.tfstate.age", dest, stamp)); err != nil {
		return fmt.Errorf("rclone upload (timestamped): %w", err)
	}
	run.Info(fmt.Sprintf("updating %s/latest.tfstate.age", dest))
	if err := run.CmdEnv(ctx.ClusterDir, rcloneEnv, "rclone", "--log-level", "ERROR", "copyto", tmpCipher, dest+"/latest.tfstate.age"); err != nil {
		return fmt.Errorf("rclone upload (latest): %w", err)
	}

	run.Ok("encrypted state backed up to Cloudflare R2")

	// Bounded storage, but never at the cost of the only copy. The prune
	// re-lists the bucket and refuses to delete anything unless the upload
	// just made is actually in that listing - "rclone exited zero" is a claim
	// about a request, not about what is in the bucket.
	pruneOldBackups(ctx, rcloneEnv, dest, stamp)

	// The private identity is deliberately absent from the config contract and
	// from OpenTofu: this program writes backups on every run and reads one
	// only when a human asks it to, from `-restore` and nowhere else.
	fmt.Printf(`
  To bring this back after a total loss, on a machine with vault access
  (an 'op signin' session, or OP_SERVICE_ACCOUNT_TOKEN exported):

    ./toolshed/contractor restore -site %s

  It fetches the identity from %s, decrypts, checks that what
  came back is state describing something, and pushes it through the encrypted
  backend. It refuses if local state already exists.

`, ctx.Site, BackupIdentityRef)

	return nil
}

// attachIfDetached initialises the workspace against the state database, but
// only when it is not initialised already.
//
// Conditional, and that is load-bearing rather than an optimisation. In a full
// ignition this phase runs after Migrate and BEFORE Sterilize, so the local
// state file is still on disk - and Attach deliberately refuses to run when it
// is, because a workspace holding local state is mid-ignition and attaching on
// top of it would leave two states describing one estate. Calling Attach
// unconditionally here would break the ignition path to fix the standalone one.
//
// So the question asked is the narrow one: has anything initialised this
// workspace? A full run reached here through Migrate and has; a standalone
// backup on a sterilized workspace has not.
func attachIfDetached(ctx *run.Context) error {
	if !needsAttach(ctx.ClusterDir) {
		return nil
	}
	run.Info("workspace is not initialised - attaching to the state database first")
	return Attach(ctx)
}

// needsAttach reports whether the workspace has no provider plugins installed.
//
// .terraform/providers is what `tofu init` populates and what `tofu state pull`
// needs; the directory alone is not enough, because a workspace can carry a
// .terraform holding only a backend record.
func needsAttach(clusterDir string) bool {
	entries, err := os.ReadDir(filepath.Join(clusterDir, ".terraform", "providers"))
	return err != nil || len(entries) == 0
}
