package onepassword

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// The subset of `op item get --format=json` this needs. Everything not named
// here is preserved verbatim, because an item carries fields this program has
// no business understanding and must not drop on the way through.
type item struct {
	Sections []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	} `json:"sections"`
	Fields []struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Label   string `json:"label"`
		Value   string `json:"value"`
		Section struct {
			ID string `json:"id"`
		} `json:"section"`
	} `json:"fields"`
}

// upsertField replaces one field's value inside an item, leaving every other
// field untouched, and returns the item to hand back to `op item edit`.
//
// Built in Go rather than with a jq filter, which is how the Ansible playbook
// does the same job. jq is fine there; here it would mean a shell pipeline
// with a secret in it and quoting rules to get right, and none of it could be
// tested without a vault. This can.
//
// Matching is on section AND label together. Two sections may both hold a
// field called "password", and matching on label alone overwrites whichever
// one happened to come first.
func upsertField(raw []byte, section, field, value string) ([]byte, error) {
	var it item
	if err := json.Unmarshal(raw, &it); err != nil {
		return nil, fmt.Errorf("parsing the item: %w", err)
	}

	// An empty section is not "unknown", it is "directly on the item" - the
	// three-segment op:// shape, which the estate's backup keypair uses. Those
	// fields carry no section object at all, so the id to match on is "".
	sectionID := ""
	if section != "" {
		for _, s := range it.Sections {
			if s.Label == section {
				sectionID = s.ID
				break
			}
		}
		if sectionID == "" {
			// Deliberately not created. A section that does not exist means
			// the reference is wrong, and inventing one puts a real credential
			// somewhere nobody will look for it.
			return nil, fmt.Errorf("the item has no section %q", section)
		}
	}

	// Rebuild rather than mutate in place: drop any existing occurrence of
	// this field in this section, then append exactly one.
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, err
	}
	rawFields, _ := generic["fields"].([]any)
	kept := make([]any, 0, len(rawFields)+1)
	for _, rf := range rawFields {
		m, ok := rf.(map[string]any)
		if !ok {
			kept = append(kept, rf)
			continue
		}
		sec, _ := m["section"].(map[string]any)
		secID, _ := sec["id"].(string)
		label, _ := m["label"].(string)
		if secID == sectionID && label == field {
			continue
		}
		kept = append(kept, rf)
	}
	added := map[string]any{
		"id":    field,
		"type":  "CONCEALED",
		"label": field,
		"value": value,
	}
	if sectionID != "" {
		added["section"] = map[string]any{"id": sectionID}
	}
	generic["fields"] = append(kept, added)
	return json.Marshal(generic)
}

// WriteField sets one concealed field, then reads it back and compares.
//
// The read-back is not belt and braces. `op item edit` accepting the request
// proves the request was accepted, not that the value which landed is the
// value sent - a wrong section id or a mangled field writes successfully to
// the wrong place. The Ansible playbook makes the same check for the same
// reason, and this is the credential that a failed write makes unrecoverable
// rather than merely wrong.
func WriteField(ref Ref, value string) error {
	get := exec.Command("op", "item", "get", ref.Item, "--vault", ref.Vault, "--format=json")
	raw, err := get.Output()
	if err != nil {
		return fmt.Errorf("reading item %s/%s: %w", ref.Vault, ref.Item, err)
	}

	updated, err := upsertField(raw, ref.Section, ref.Field, value)
	if err != nil {
		return fmt.Errorf("updating %s: %w", ref, err)
	}

	// Piped on stdin, never as an argument: command lines are visible to
	// every other process on the machine.
	edit := exec.Command("op", "item", "edit", ref.Item, "--vault", ref.Vault)
	edit.Stdin = strings.NewReader(string(updated))
	if out, err := edit.CombinedOutput(); err != nil {
		return fmt.Errorf("writing %s: %w: %s", ref, err, strings.TrimSpace(string(out)))
	}

	back, err := Read(ref.String())
	if err != nil {
		return fmt.Errorf("reading back %s to verify the write: %w", ref, err)
	}
	if back != value {
		return fmt.Errorf("%s did not round-trip: the value written and the value read back differ", ref)
	}
	return nil
}

// EnsureField returns the value at ref, generating and storing one if the
// field is absent or empty.
//
// Generate-if-absent rather than rotate-every-run, on purpose. Rotating a
// credential that OpenTofu state already depends on - the state database
// password most of all - means changing it underneath a run that is using it
// to reach that state. Rotation belongs at a point where nothing is mid-flight;
// this is the part that makes a brand new site need no human to invent a
// password nobody will ever read.
func EnsureField(ref Ref, generate func() (string, error)) (string, string, error) {
	existing, err := Read(ref.String())
	if err == nil && strings.TrimSpace(existing) != "" {
		return existing, "existing", nil
	}

	value, err := generate()
	if err != nil {
		return "", "", fmt.Errorf("generating a value for %s: %w", ref, err)
	}
	if err := WriteField(ref, value); err != nil {
		return "", "", err
	}
	return value, "generated", nil
}
