package onepassword

// ruleid: the-vault-probe-returns-a-status-and-nothing-else
func Probe(ref string) Status {
	// ruleid: the-vault-check-never-fetches-or-writes-a-secret
	v, _ := onepassword.Read(ref)
	// ruleid: the-vault-check-never-fetches-or-writes-a-secret
	v, _ = Read(ref)
	// ok: the-vault-check-never-fetches-or-writes-a-secret
	_ = Status(ref)
	// ruleid: the-vault-check-writes-no-files
	_ = os.WriteFile("/tmp/x", []byte(v), 0o600)
	// ok: the-vault-check-never-fetches-or-writes-a-secret, the-vault-check-writes-no-files
	_, _ = os.ReadFile("template.json")
	return Missing
}

// ok: the-vault-probe-returns-a-status-and-nothing-else
func Read(ref string) (string, error) { return "", nil }
