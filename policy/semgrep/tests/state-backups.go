package phases

func phases(cfg any, site, name string) {
	// ruleid: nothing-resolves-the-state-backups-by-hand
	_ = config.LatestStateBackupPath(site)
	// ruleid: nothing-resolves-the-state-backups-by-hand
	_, _ = config.BucketByKey("state")
	// ruleid: backup-reaches-the-state-backups-through-the-resolver, restore-reaches-the-state-backups-through-the-resolver
	_, _ = config.StateBackupLocation(cfg, site, name)
	// ruleid: the-teardown-resolves-the-bucket-it-empties-from-the-table
	_, _ = config.BucketByKey("database")
}
