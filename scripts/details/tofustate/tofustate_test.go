package tofustate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestManagedLeavesOutDataSources(t *testing.T) {
	st, err := Parse([]byte(`{"version":4,"serial":3,"lineage":"l","resources":[
		{"mode":"data","type":"d","name":"a"},
		{"mode":"managed","type":"t","name":"b"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(st.Managed(), " "); got != "t.b" {
		t.Errorf("got %q, want t.b", got)
	}
	if st.Version != 4 || st.Serial != 3 || st.Lineage != "l" || len(st.Resources) != 2 {
		t.Errorf("got %+v", st)
	}
	if _, err := Parse([]byte("not json")); err == nil {
		t.Error("something that is not JSON parsed as state")
	}
}

func TestAPlanSaysWhatItCreatesAndItsPlannedNames(t *testing.T) {
	p, err := ParsePlan([]byte(`{"format_version":"1.2","resource_changes":[
		{"address":"a.x","type":"a","change":{"actions":["create"],"after":{"name":"n","size":3}}},
		{"address":"a.y","type":"a","change":{"actions":["delete","create"],"after":{"name":"m"}}}],
		"output_changes":{"o":{"actions":["update"],"after_sensitive":true}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !p.ResourceChanges[0].Creates() || p.ResourceChanges[1].Creates() {
		t.Error("a replacement was read as a create, or a create was not")
	}
	if name, ok := p.ResourceChanges[0].AfterString("name"); !ok || name != "n" {
		t.Errorf("got %q, %v", name, ok)
	}
	if _, ok := p.ResourceChanges[0].AfterString("size"); ok {
		t.Error("a number was read as a string")
	}
	if _, ok := p.ResourceChanges[0].AfterString("missing"); ok {
		t.Error("an absent attribute was read as known")
	}
	if !p.OutputChanges["o"].Sensitive() {
		t.Error("an output marked sensitive after the change was not reported sensitive")
	}
}

// What a change would alter is said by attribute name, and only where the
// value differs: spacing is not a change, absent and null are the same, and
// what is known only after the apply is one.
func TestAChangeIsSaidByTheAttributesItAlters(t *testing.T) {
	plan := []byte(`{"resource_changes": [{"address": "a.b", "mode": "managed", "change": {
		"actions": ["update"],
		"before": {"same": {"x": 1}, "spaced": [1,2], "moved": "old-secret", "gone": "v", "nulled": null},
		"after": {"same": {"x":1}, "spaced": [ 1, 2 ], "moved": "new-secret", "added": "w"},
		"after_unknown": {"computed": true, "settled": false, "known": null}
	}}]}`)
	p, err := ParsePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	got := p.ResourceChanges[0].ChangedAttributes()
	want := []string{"added", "computed", "gone", "moved"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("altered %v, want %v", got, want)
	}
	for _, name := range got {
		if strings.Contains(name, "secret") {
			t.Errorf("a value was said: %v", got)
		}
	}
}

// An encoding that is not JSON is compared as it is written, so two
// different ones are never read as the same value.
func TestEncodingsThatAreNotJSONAreComparedAsWritten(t *testing.T) {
	if sameJSON(json.RawMessage("{not"), json.RawMessage("{nope")) {
		t.Error("two different encodings that are not JSON were read as the same")
	}
	if !sameJSON(json.RawMessage("{not"), json.RawMessage("{not")) {
		t.Error("the same encoding was read as a change")
	}
}
