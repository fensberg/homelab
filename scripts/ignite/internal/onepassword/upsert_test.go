package onepassword

import (
	"encoding/json"
	"testing"
)

const itemJSON = `{
  "id": "itemid",
  "title": "site0",
  "vault": {"id": "vaultid", "name": "homelab"},
  "sections": [
    {"id": "sec-hyp", "label": "hypervisor"},
    {"id": "sec-db",  "label": "database"}
  ],
  "fields": [
    {"id": "token_id", "type": "STRING", "label": "token_id", "value": "keep-me", "section": {"id": "sec-hyp"}},
    {"id": "password", "type": "CONCEALED", "label": "password", "value": "OLD", "section": {"id": "sec-db"}}
  ]
}`

func fieldsOf(t *testing.T, b []byte) []item2Field {
	t.Helper()
	var it item
	if err := json.Unmarshal(b, &it); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	out := make([]item2Field, 0, len(it.Fields))
	for _, f := range it.Fields {
		out = append(out, item2Field{f.Label, f.Value, f.Section.ID})
	}
	return out
}

type item2Field struct{ label, value, section string }

func find(fs []item2Field, label, section string) (item2Field, bool) {
	for _, f := range fs {
		if f.label == label && f.section == section {
			return f, true
		}
	}
	return item2Field{}, false
}

func TestUpsertField_ReplacesInPlace(t *testing.T) {
	out, err := upsertField([]byte(itemJSON), "database", "password", "NEW")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fs := fieldsOf(t, out)
	got, ok := find(fs, "password", "sec-db")
	if !ok {
		t.Fatal("password is missing after the upsert")
	}
	if got.value != "NEW" {
		t.Errorf("value = %q, want NEW", got.value)
	}
	// Exactly one, or op keeps both and a later read is a coin flip.
	n := 0
	for _, f := range fs {
		if f.label == "password" && f.section == "sec-db" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("password appears %d times; the old value was not removed", n)
	}
}

// The blast radius of this function is every other secret in the item.
func TestUpsertField_LeavesEverythingElseAlone(t *testing.T) {
	out, err := upsertField([]byte(itemJSON), "database", "password", "NEW")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := find(fieldsOf(t, out), "token_id", "sec-hyp")
	if !ok || got.value != "keep-me" {
		t.Errorf("an unrelated field was disturbed: %+v (present=%v)", got, ok)
	}
}

func TestUpsertField_AddsAMissingField(t *testing.T) {
	out, err := upsertField([]byte(itemJSON), "database", "encryption_passphrase", "age1xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := find(fieldsOf(t, out), "encryption_passphrase", "sec-db")
	if !ok || got.value != "age1xyz" {
		t.Errorf("new field not added correctly: %+v (present=%v)", got, ok)
	}
	if _, ok := find(fieldsOf(t, out), "password", "sec-db"); !ok {
		t.Error("adding a field removed a sibling")
	}
}

// Two sections can hold fields with the same label. Matching on label alone
// would overwrite the wrong one.
func TestUpsertField_IsScopedToItsSection(t *testing.T) {
	withDup := `{"id":"i","title":"site0","vault":{"id":"v","name":"homelab"},
	  "sections":[{"id":"a","label":"one"},{"id":"b","label":"two"}],
	  "fields":[
	    {"id":"p","type":"CONCEALED","label":"password","value":"A","section":{"id":"a"}},
	    {"id":"p","type":"CONCEALED","label":"password","value":"B","section":{"id":"b"}}]}`
	out, err := upsertField([]byte(withDup), "two", "password", "CHANGED")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fs := fieldsOf(t, out)
	if got, _ := find(fs, "password", "a"); got.value != "A" {
		t.Errorf("the other section's password was changed to %q", got.value)
	}
	if got, _ := find(fs, "password", "b"); got.value != "CHANGED" {
		t.Errorf("target not updated, got %q", got.value)
	}
}

func TestUpsertField_RefusesAnUnknownSection(t *testing.T) {
	if _, err := upsertField([]byte(itemJSON), "not_a_section", "x", "y"); err == nil {
		t.Error("expected an error for a section that does not exist; creating one silently would put the secret somewhere nobody looks")
	}
}

func TestUpsertField_RefusesGarbageInput(t *testing.T) {
	if _, err := upsertField([]byte("not json"), "database", "password", "x"); err == nil {
		t.Error("expected an error for input that is not an item")
	}
}

// The estate's backup keypair sits directly on its item, with no section at
// all - the three-segment op:// shape. That field carries no "section" object,
// so matching on an empty section id is what finds it, and the replacement
// must not grow one.
const sectionlessItemJSON = `{
  "id": "itemid",
  "title": "state_backup",
  "vault": {"id": "vaultid", "name": "homelab"},
  "fields": [
    {"id": "recipient", "type": "CONCEALED", "label": "recipient", "value": "OLD"},
    {"id": "identity", "type": "CONCEALED", "label": "identity", "value": "keep-me"}
  ]
}`

func TestUpsertField_NoSection(t *testing.T) {
	out, err := upsertField([]byte(sectionlessItemJSON), "", "recipient", "age1new")
	if err != nil {
		t.Fatalf("upsertField: %v", err)
	}
	fs := fieldsOf(t, out)

	got, ok := find(fs, "recipient", "")
	if !ok || got.value != "age1new" {
		t.Fatalf("recipient did not take the new value, got %+v", fs)
	}
	n := 0
	for _, f := range fs {
		if f.label == "recipient" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("recipient appears %d times; the old value was not removed", n)
	}
	if other, ok := find(fs, "identity", ""); !ok || other.value != "keep-me" {
		t.Error("the sibling field was not preserved")
	}
}

// A field on the item and a same-named field inside a section are different
// fields. Writing the section-less one must not reach into the section.
func TestUpsertField_NoSectionDoesNotTouchSameNameInSection(t *testing.T) {
	mixed := `{
  "sections": [{"id": "sec-db", "label": "database"}],
  "fields": [
    {"id": "recipient", "type": "CONCEALED", "label": "recipient", "value": "TOP"},
    {"id": "recipient2", "type": "CONCEALED", "label": "recipient", "value": "IN-SECTION", "section": {"id": "sec-db"}}
  ]
}`
	out, err := upsertField([]byte(mixed), "", "recipient", "NEW")
	if err != nil {
		t.Fatalf("upsertField: %v", err)
	}
	fs := fieldsOf(t, out)
	if top, ok := find(fs, "recipient", ""); !ok || top.value != "NEW" {
		t.Errorf("the item-level field was not updated, got %+v", fs)
	}
	if inSec, ok := find(fs, "recipient", "sec-db"); !ok || inSec.value != "IN-SECTION" {
		t.Errorf("the sectioned field of the same name was disturbed, got %+v", fs)
	}
}
