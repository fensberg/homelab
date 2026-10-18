package phases

import "homelab/details/onepassword"

func phase() {
	// ruleid: the-break-glass-identity-is-read-only-by-the-restore-path
	_, _ = onepassword.Read(BackupIdentityRef)
	ref := "op://estate/state_backup/identity"
	// ruleid: the-break-glass-identity-is-read-only-by-the-restore-path
	_, _ = onepassword.Read(ref)
	// ok: the-break-glass-identity-is-read-only-by-the-restore-path
	_, _ = onepassword.Read(BackupRecipientRef)
	// ok: the-break-glass-identity-is-read-only-by-the-restore-path
	warn("fetch it from " + BackupIdentityRef + " by hand")
}
