package tofustate

import "encoding/json"

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
