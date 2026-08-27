package onepassword

import "testing"

// Writing a secret needs the reference broken into its parts, because
// `op item edit` addresses a vault, an item, a section and a field
// separately. Getting that wrong does not fail - it writes a real secret
// into the wrong place and reports success.

func TestParseRef_WithSection(t *testing.T) {
	r, err := ParseRef("op://homelab/site0/state_database/db_password")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Vault != "homelab" || r.Item != "site0" || r.Section != "state_database" || r.Field != "db_password" {
		t.Errorf("got %+v", r)
	}
}

func TestParseRef_WithoutSection(t *testing.T) {
	r, err := ParseRef("op://homelab/source-control/token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Vault != "homelab" || r.Item != "source-control" || r.Field != "token" {
		t.Errorf("got %+v", r)
	}
	if r.Section != "" {
		t.Errorf("Section should be empty for a two-segment path, got %q", r.Section)
	}
}

func TestParseRef_Rejects(t *testing.T) {
	for _, bad := range []string{
		"",
		"homelab/site0/db_password",        // no scheme
		"op://homelab",                     // vault only
		"op://homelab/site0",               // no field
		"op://homelab/site0/a/b/c",         // too deep for op item edit
		"op:///site0/db_password",          // empty vault
		"op://homelab//db_password",        // empty item
		"http://homelab/site0/db_password", // wrong scheme
	} {
		if _, err := ParseRef(bad); err == nil {
			t.Errorf("ParseRef(%q) was accepted", bad)
		}
	}
}

// The reference must survive a round trip, or a value written through the
// parsed parts is not the value read back through the original reference.
func TestRef_StringRoundTrips(t *testing.T) {
	for _, ref := range []string{
		"op://homelab/site0/state_database/db_password",
		"op://homelab/source-control/token",
	} {
		r, err := ParseRef(ref)
		if err != nil {
			t.Fatalf("%s: %v", ref, err)
		}
		if r.String() != ref {
			t.Errorf("round trip changed %q into %q", ref, r.String())
		}
	}
}
