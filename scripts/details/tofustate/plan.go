package tofustate

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
)

// Plan is the part of `tofu show -json <plan>` the programs here read: the
// contractor to summarise a plan without printing a value, the lawyer to see
// which buckets a build would create.
//
// FormatVersion is not decoration. Every field below is optional in JSON - a
// document with none of them unmarshals cleanly into an empty struct, and an
// empty struct used to render "No changes. The estate already matches the
// config.", which is a positive claim about reality made from having read
// nothing. A plan document always carries a format version, so requiring it
// is what separates "this plan holds no changes" from "this is not a plan".
type Plan struct {
	FormatVersion   string                  `json:"format_version"`
	ResourceChanges []ResourceChange        `json:"resource_changes"`
	OutputChanges   map[string]OutputChange `json:"output_changes"`
}

// ResourceChange is one resource the plan touches.
type ResourceChange struct {
	Address string `json:"address"`
	Mode    string `json:"mode"`
	Type    string `json:"type"`
	Change  struct {
		Actions []string `json:"actions"`
		// Top-level attributes only, and never their contents. A reader that
		// goes deeper is one that can print a value.
		Before       map[string]json.RawMessage `json:"before"`
		After        map[string]json.RawMessage `json:"after"`
		AfterUnknown map[string]json.RawMessage `json:"after_unknown"`
		// Which attributes forced a replacement. "This machine is being
		// rebuilt" and "this machine is being rebuilt BECAUSE ITS DISK
		// CHANGED" are different decisions.
		ReplacePaths [][]json.RawMessage `json:"replace_paths"`
	} `json:"change"`
}

// OutputChange is one output the plan touches.
type OutputChange struct {
	Actions         []string        `json:"actions"`
	BeforeSensitive json.RawMessage `json:"before_sensitive"`
	AfterSensitive  json.RawMessage `json:"after_sensitive"`
}

// Sensitive reports whether tofu marked either side of this output secret.
// Both sides matter: an output that stops being sensitive is still one whose
// old value must not be printed.
func (o OutputChange) Sensitive() bool {
	return string(o.BeforeSensitive) == "true" || string(o.AfterSensitive) == "true"
}

// ParsePlan reads a plan shown as JSON.
func ParsePlan(body []byte) (Plan, error) {
	var p Plan
	err := json.Unmarshal(body, &p)
	return p, err
}

// Creates reports whether a change creates its resource and does nothing else.
func (c ResourceChange) Creates() bool {
	return len(c.Change.Actions) == 1 && c.Change.Actions[0] == "create"
}

// AfterString is a top-level string attribute's planned value, and whether it
// is known as a string at plan time.
func (c ResourceChange) AfterString(attr string) (string, bool) {
	raw, ok := c.Change.After[attr]
	if !ok {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// ChangedAttributes is the name of every top-level attribute a change would
// alter: one whose value differs before and after, or one known only after
// the apply. Names only, sorted, and never a value. The contractor's plan
// comment and the lawyer's log both say an update by these.
//
// WHY NAMES ARE SAFE AND VALUES ARE NOT. A top-level key in a plan's before or
// after object is a provider schema attribute - `memory`, `cpu`, `disk`. Those
// are public API surface, identical in every estate that uses the provider,
// and they carry nothing about THIS estate. The values under them are the
// hostnames, addresses and credentials this repository keeps out of git, and
// what these feed is world-readable.
//
// SO THE DEPTH LIMIT IS THE SAFETY PROPERTY, not a simplification. One level
// down, map-typed attributes have operator-supplied keys - a label, an
// annotation, a tag - and those can be a real hostname. So this never
// descends, and must not be "improved" to.
//
// after_unknown marks an attribute whose value is not computable until apply.
// It counts as changing: unknown-at-plan is how a change that depends on
// something being created shows up. An attribute absent on one side and null
// on the other holds no value either way, and is not a change.
func (c ResourceChange) ChangedAttributes() []string {
	names := map[string]bool{}
	for name, after := range c.Change.After {
		if !sameJSON(c.Change.Before[name], after) {
			names[name] = true
		}
	}
	for name, before := range c.Change.Before {
		if !sameJSON(before, c.Change.After[name]) {
			names[name] = true
		}
	}
	// after_unknown carries false, or null, for an attribute that is known,
	// and for a list or an object the same shape as its value, with true only
	// where something in it is not known: [{}, {}] is a list of two entries
	// all known. So an attribute is unknown when true is somewhere in it, and
	// its presence alone proves nothing.
	for name, unknown := range c.Change.AfterUnknown {
		if holdsTrue(unknown) {
			names[name] = true
		}
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// holdsTrue reports whether true is anywhere in an encoding: the value
// itself, or any element or member at any depth. One that cannot be read
// holds whatever it might, so it is taken as holding true.
func holdsTrue(raw json.RawMessage) bool {
	var v any
	if len(raw) == 0 {
		return false
	}
	if json.Unmarshal(raw, &v) != nil {
		return true
	}
	var walk func(any) bool
	walk = func(v any) bool {
		switch x := v.(type) {
		case bool:
			return x
		case []any:
			for _, e := range x {
				if walk(e) {
					return true
				}
			}
		case map[string]any:
			for _, e := range x {
				if walk(e) {
					return true
				}
			}
		}
		return false
	}
	return walk(v)
}

// sameJSON reports whether two encodings say the same thing, however they
// are spaced. An attribute absent on one side and null on the other is the
// same: neither holds a value.
func sameJSON(a, b json.RawMessage) bool {
	var x, y any
	if len(a) == 0 {
		a = json.RawMessage("null")
	}
	if len(b) == 0 {
		b = json.RawMessage("null")
	}
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return bytes.Equal(a, b)
	}
	return reflect.DeepEqual(x, y)
}
