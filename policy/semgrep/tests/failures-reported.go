package phases

func teardown() error {
	if err := destroy(); err != nil {
		// ruleid: no-phase-swallows-an-error-it-handles
		run.Warn("destroy failed; check Proxmox manually")
		return nil
	}
	if err := destroy(); err != nil {
		// ok: no-phase-swallows-an-error-it-handles
		run.Warn("destroy failed: " + err.Error())
	}
	if err := destroy(); err != nil {
		run.Warn("could not migrate: " + err.Error())
		// ok: no-phase-swallows-an-error-it-handles
		run.Warn("the fallback is the age backup in object storage")
		return nil
	}
	if err := destroy(); err != nil {
		// ok: no-phase-swallows-an-error-it-handles
		run.Warn("destroy failed; check Proxmox manually")
		return fmt.Errorf("destroying: %w", err)
	}
	return nil
}
