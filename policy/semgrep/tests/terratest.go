package integration

func options(dir string) {
	// ruleid: terraform-options-are-built-in-the-harness, the-harness-silences-terratests-logger
	_ = &terraform.Options{TerraformDir: dir, Logger: logger.Discard}
	// ruleid: terraform-options-are-built-in-the-harness
	_ = &terraform.Options{TerraformDir: dir}
	// ok: terraform-options-are-built-in-the-harness, the-harness-silences-terratests-logger
	_ = harness.TofuOptions(dir)
}
