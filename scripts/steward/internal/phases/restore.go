package phases

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"homelab/steward/internal/config"
	"homelab/steward/internal/onepassword"
	"homelab/steward/internal/run"
)

// Restore brings the age-encrypted state back from object storage.
//
// It is the other half of Backup, and until now it existed only as three
// commands printed at the end of a successful run. A recipe is not a restore.
// It has to be retyped correctly under the one circumstance where nobody is
// calm - the cluster is gone, and the state describing how to rebuild it went
// with it - and this project's own history says that is exactly when a vault
// path gets misspelled.
//
// # What this is for
//
// The state database backs itself up to object storage continuously, and those
// backups restore into a running cluster. This one does not need a cluster,
// which is the whole point: it is what breaks the circle after a total loss,
// where rebuilding the cluster needs the state that only exists inside it.
//
// # What it does not do
//
// It does not rebuild anything. It puts the state back where OpenTofu expects
// it and stops, because what to do next - re-ignite, destroy, or just look -
// is a judgement call a human makes with the facts in front of them.
func Restore(ctx *run.Context) error {
	run.WritePhase("Restore", "Bring the age-encrypted state back from object storage.")

	// Refuse before doing anything else. Restoring over live state replaces
	// the description of a running estate with an older one, and the estate
	// does not change to match - every resource created since that backup
	// becomes something nothing is tracking.
	if _, err := os.Stat(ctx.LocalState); err == nil {
		return fmt.Errorf(`there is already local state at %s, so this refuses to run.

Restoring over it would replace the description of whatever is running now with
an older one, and nothing in Proxmox would change to match. If the local state
is genuinely stale, move it aside first and decide deliberately:

    mv %s %s.superseded`, ctx.LocalState, ctx.LocalState, ctx.LocalState)
	}

	// Same credential check as Destroy: no vault session means no bucket
	// credentials and no identity, so the command is inert without one.
	run.Info("rendering the config (this is the credential check)")
	if err := Render(ctx); err != nil {
		return fmt.Errorf("could not render the config, so there is nothing to authenticate with: %w", err)
	}

	cfg, err := config.LoadRendered(ctx.ConfigRendered)
	if err != nil {
		return err
	}
	site, ok := cfg.Sites[ctx.Site]
	if !ok {
		return fmt.Errorf("unknown site '%s'", ctx.Site)
	}
	store := site.ObjectStorage
	if strings.TrimSpace(store.Bucket) == "" {
		return fmt.Errorf("sites.%s.object_storage.bucket is missing from the rendered config", ctx.Site)
	}

	for _, tool := range []string{"age", "rclone"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("'%s' not found on PATH", tool)
		}
	}

	rcloneEnv := r2Env(cfg.ObjectStorage, store)
	key := backupObjectKey(store.Bucket)

	// Show what else is there before restoring. The timestamped objects are
	// the only record of which runs produced which state, and an operator
	// deciding whether "latest" is the one they want needs to see them.
	listBackups(ctx, rcloneEnv, store.Bucket)

	run.Info("fetching " + key)
	cipher, err := run.CmdBytes(ctx.ClusterDir, rcloneEnv, nil, "rclone", "cat", key)
	if err != nil {
		return fmt.Errorf(`could not fetch %s: %w

If the bucket is empty, no run ever completed its Backup phase against it - or
the estate was torn down, which deletes the bucket and everything in it`, key, err)
	}
	if len(cipher) == 0 {
		return fmt.Errorf("%s is empty", key)
	}

	plain, err := decryptWithBreakGlassIdentity(ctx, cipher)
	defer run.Wipe(plain)
	if err != nil {
		return err
	}

	summary, err := validateRestoredState(plain)
	if err != nil {
		return err
	}
	run.Ok(fmt.Sprintf("decrypted state: serial %d, %d resources, lineage %s",
		summary.Serial, summary.Resources, summary.Lineage))

	// Pushed rather than written. State at rest is encrypted (see
	// encryption.go), and what comes out of the age file is the plaintext
	// `tofu state pull` produced - so writing it straight to terraform.tfstate
	// would produce a file tofu then refuses to read. `state push` puts it
	// through the configured backend, which is what encrypts it.
	run.Info("initialising the local backend")
	if err := run.TofuInit(ctx); err != nil {
		return err
	}
	run.Info("pushing the restored state through the encrypted backend")
	if err := run.CmdStdin(ctx.ClusterDir, plain, "tofu", "state", "push", "-"); err != nil {
		return fmt.Errorf("tofu state push: %w", err)
	}

	run.Ok("state restored to " + ctx.LocalState)
	fmt.Printf(`
  It is encrypted at rest and describes %d resources. Nothing has been changed
  in Proxmox, Cloudflare or the tailnet - this only put the state back.

  What usually comes next:

    tofu -chdir=management/cluster plan   # see how far reality has drifted
    ./scripts/steward/steward destroy -site %s -confirm %s

`, summary.Resources, ctx.Site, ctx.Site)
	return nil
}

// backupObjectKey addresses the rolling copy the Backup phase overwrites every
// run. The timestamped objects beside it are listed for the operator rather
// than selectable here: for OpenTofu state, older is not a point in time worth
// rewinding to - it describes fewer resources than exist, which is the one
// thing more dangerous than no state at all.
func backupObjectKey(bucket string) string {
	return "R2:" + bucket + "/management-cluster/latest.tfstate.age"
}

func listBackups(ctx *run.Context, env []string, bucket string) {
	out, err := run.CmdOutputEnv(ctx.ClusterDir, env, "rclone", "lsl", "R2:"+bucket+"/management-cluster")
	if err != nil || strings.TrimSpace(out) == "" {
		return
	}
	run.Info("backups in the bucket:")
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			fmt.Println("      " + strings.TrimSpace(line))
		}
	}
}

// decryptWithBreakGlassIdentity fetches the private half and decrypts.
//
// age can only take an identity from a file, so one is written - 0600, zeroed
// and removed before this returns. That is worse than never touching disk and
// better than the alternative it replaces, which was a printed instruction to
// `op read` the key into /tmp and remember to delete it.
//
// This is the only place in the program that reads the identity at all. Every
// other phase works with the recipient, which is public.
func decryptWithBreakGlassIdentity(ctx *run.Context, cipher []byte) ([]byte, error) {
	identity, err := onepassword.Read(BackupIdentityRef)
	if err != nil || strings.TrimSpace(identity) == "" {
		return nil, fmt.Errorf(`could not read the break-glass identity from %s.

Without it the backup cannot be decrypted by anyone, including whoever wrote
it - that is the property it exists to have. If this estate's backups were
encrypted to a key held somewhere else, decrypt by hand:

    rclone cat R2:<bucket>/management-cluster/latest.tfstate.age | age -d -i <key>`, BackupIdentityRef)
	}

	f, err := os.CreateTemp("", "ignite-identity-*")
	if err != nil {
		return nil, err
	}
	path := f.Name()
	defer func() {
		// Zero the bytes on disk before unlinking, not merely unlink: this
		// project's own rule is that deletion is not the security property.
		if st, statErr := os.Stat(path); statErr == nil {
			_ = os.WriteFile(path, make([]byte, st.Size()), 0o600)
		}
		_ = os.Remove(path)
	}()

	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.WriteString(identity); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}

	run.Info("decrypting with the break-glass identity")
	plain, err := run.CmdBytes(ctx.ClusterDir, nil, cipher, "age", "-d", "-i", path)
	if err != nil {
		return nil, fmt.Errorf(`age could not decrypt the backup: %w

The stored identity does not match the recipient this backup was encrypted to.
Rotating the keypair strands every backup written under the previous one`, err)
	}
	return plain, nil
}

// stateSummary is what a restore can honestly say about what came back,
// without printing any of it.
type stateSummary struct {
	Serial    int
	Lineage   string
	Resources int
}

// validateRestoredState checks that what came out of age is state describing
// something, before any of it is written anywhere.
//
// Every failure below is one that otherwise reports success. age exits zero on
// an empty stream. A wrong object decrypts to something that is not state. And
// state with no resources in it is exactly what an empty workspace produces -
// pushing that over a real backend turns a restore into a deletion.
func validateRestoredState(body []byte) (stateSummary, error) {
	if len(body) == 0 {
		return stateSummary{}, fmt.Errorf("the restore produced no data at all")
	}

	var st struct {
		Version   int               `json:"version"`
		Serial    int               `json:"serial"`
		Lineage   string            `json:"lineage"`
		Resources []json.RawMessage `json:"resources"`
	}
	if err := json.Unmarshal(body, &st); err != nil {
		return stateSummary{}, fmt.Errorf(`what came back is not JSON, so it is not OpenTofu state.

The usual cause is that the object was never state - or that it was never
decrypted, which happens when the identity is wrong: %w`, err)
	}
	if st.Version == 0 || st.Lineage == "" {
		return stateSummary{}, fmt.Errorf("the decrypted JSON has no version or lineage, so it is not OpenTofu state")
	}
	if len(st.Resources) == 0 {
		return stateSummary{}, fmt.Errorf(`the restored state has no resources in it.

That is what an empty workspace produces. Pushing it would replace a real state
file with a description of nothing, which is a deletion rather than a restore`)
	}
	return stateSummary{Serial: st.Serial, Lineage: st.Lineage, Resources: len(st.Resources)}, nil
}
