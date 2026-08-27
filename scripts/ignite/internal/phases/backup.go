package phases

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"homelab/ignite/internal/config"
	"homelab/ignite/internal/run"
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

	for field, val := range map[string]string{
		"account_id":        store.AccountID,
		"access_key_id":     store.AccessKeyID,
		"secret_access_key": store.SecretAccessKey,
		"bucket":            store.Bucket,
	} {
		if strings.TrimSpace(val) == "" {
			return fmt.Errorf("sites.%s.object_storage.%s is missing from the rendered config", ctx.Site, field)
		}
	}
	if strings.TrimSpace(site.State.BackupRecipient) == "" {
		return fmt.Errorf(`no 'state.backup_recipient' for site %s in the rendered config.

State is never uploaded in plaintext. Generate a key pair once:

    age-keygen -o state-backup.key

Put the PUBLIC recipient (the 'age1...' line) in 1Password at
op://homelab/site<N>-state-database/backup_recipient, and store the private
key file somewhere offline. The automation only ever needs the public half -
it can write backups but cannot read them back.`, ctx.Site)
	}

	for _, tool := range []string{"age", "rclone"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("'%s' not found on PATH", tool)
		}
	}

	stamp := time.Now().Format("20060102-150405")
	// Only the ciphertext ever becomes a file. The plaintext is held in
	// memory and piped straight into age.
	tmpCipher := filepath.Join(os.TempDir(), fmt.Sprintf("tofu-state-%s.json.age", stamp))
	defer os.Remove(tmpCipher)

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
	run.Info("pulling the current state")
	state, err := run.CmdOutputBytes(ctx.ClusterDir, "tofu", "state", "pull")
	defer run.Wipe(state)
	if err != nil {
		return fmt.Errorf("tofu state pull: %w", err)
	}
	if len(state) < 100 {
		return fmt.Errorf("state pull returned only %d bytes - refusing to upload it", len(state))
	}

	recipientPreview := site.State.BackupRecipient
	if len(recipientPreview) > 20 {
		recipientPreview = recipientPreview[:20]
	}
	run.Info("encrypting to " + recipientPreview + "...")
	if err := run.CmdStdin(ctx.ClusterDir, state, "age",
		"--recipient", site.State.BackupRecipient, "--output", tmpCipher); err != nil {
		return fmt.Errorf("age encrypt: %w", err)
	}
	run.Wipe(state)

	// rclone is configured entirely through environment variables scoped to
	// this process, so no credentials are ever written to a config file on
	// disk.
	rcloneEnv := []string{
		"RCLONE_CONFIG_R2_TYPE=s3",
		"RCLONE_CONFIG_R2_PROVIDER=Cloudflare",
		"RCLONE_CONFIG_R2_ACCESS_KEY_ID=" + store.AccessKeyID,
		"RCLONE_CONFIG_R2_SECRET_ACCESS_KEY=" + store.SecretAccessKey,
		"RCLONE_CONFIG_R2_ENDPOINT=https://" + store.AccountID + ".r2.cloudflarestorage.com",
		"RCLONE_CONFIG_R2_NO_CHECK_BUCKET=true",
	}

	dest := fmt.Sprintf("R2:%s/management-cluster", store.Bucket)
	run.Info(fmt.Sprintf("uploading to %s/%s.tfstate.age", dest, stamp))
	if err := run.CmdEnv(ctx.ClusterDir, rcloneEnv, "rclone", "copyto", tmpCipher, fmt.Sprintf("%s/%s.tfstate.age", dest, stamp)); err != nil {
		return fmt.Errorf("rclone upload (timestamped): %w", err)
	}
	run.Info(fmt.Sprintf("updating %s/latest.tfstate.age", dest))
	if err := run.CmdEnv(ctx.ClusterDir, rcloneEnv, "rclone", "copyto", tmpCipher, dest+"/latest.tfstate.age"); err != nil {
		return fmt.Errorf("rclone upload (latest): %w", err)
	}

	run.Ok("encrypted state backed up to Cloudflare R2")

	// Bounded storage, but never at the cost of the only copy. The prune
	// re-lists the bucket and refuses to delete anything unless the upload
	// just made is actually in that listing - "rclone exited zero" is a claim
	// about a request, not about what is in the bucket.
	pruneOldBackups(ctx, rcloneEnv, dest, stamp)

	// The private identity is deliberately absent from the config contract:
	// this program can write backups and must not be able to read them. It
	// is fetched by a human, by hand, only when restoring.
	fmt.Printf(`
  To restore, on a machine with op signed in:

    op read "op://homelab/%s/state-database/backup_identity" > /tmp/restore.key
    rclone cat R2:%s/management-cluster/latest.tfstate.age |
        age -d -i /tmp/restore.key > terraform.tfstate
    rm /tmp/restore.key

`, ctx.Site, store.Bucket)

	return nil
}
