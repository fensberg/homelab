package asbuilt

import (
	"encoding/json"
	"fmt"
)

// Fold writes an offline plan's differences back into the record, so that a
// plan of the same inputs against it is quiet.
//
// SAFE ONLY WHEN THE ESTATE IS KNOWN TO BE CONVERGED. Every difference is then
// an artefact of the replacements - a value computed from a fingerprint rather
// than from the real value - and folding it in is what makes the record agree
// with itself. Against an estate with real pending changes this would write
// those changes into the record as though they had been built, so the Record
// phase refuses to start unless the real plan is empty.
//
// Returns how many resource instances and outputs were changed.
func Fold(state, plan map[string]any) (int, error) {
	idx := map[string]map[string]any{}
	resources, _ := state["resources"].([]any)
	for _, r := range resources {
		res, _ := r.(map[string]any)
		insts, _ := res["instances"].([]any)
		for _, i := range insts {
			inst, _ := i.(map[string]any)
			idx[instanceKey(res["module"], res["mode"], res["type"], res["name"], inst["index_key"])] = inst
		}
	}

	folded := 0
	changes, _ := plan["resource_changes"].([]any)
	for _, c := range changes {
		rc, _ := c.(map[string]any)
		if rc["mode"] != "managed" {
			continue
		}
		change, _ := rc["change"].(map[string]any)
		if quietActions(change["actions"]) {
			continue
		}
		inst, ok := idx[instanceKey(rc["module_address"], "managed", rc["type"], rc["name"], rc["index"])]
		if !ok {
			// A create: the record has nothing for it to fold into. It stays
			// in the plan as a residual, which is what it is.
			continue
		}
		attrs, _ := inst["attributes"].(map[string]any)
		after, _ := change["after"].(map[string]any)
		inst["attributes"] = merge(attrs, after, change["after_unknown"])
		inst["sensitive_attributes"] = sensitivePaths(change["after_sensitive"], nil, []any{})
		folded++
	}

	outputs, _ := state["outputs"].(map[string]any)
	outChanges, _ := plan["output_changes"].(map[string]any)
	for name, c := range outChanges {
		change, _ := c.(map[string]any)
		if quietActions(change["actions"]) || change["after_unknown"] == true {
			continue
		}
		out, ok := outputs[name].(map[string]any)
		if !ok {
			continue // a new output has no type to keep; it stays a residual
		}
		out["value"] = change["after"]
		out["sensitive"] = change["after_sensitive"] == true
		folded++
	}

	serial, _ := state["serial"].(json.Number)
	n, err := serial.Int64()
	if err != nil {
		return 0, fmt.Errorf("the record's serial is not a number: %w", err)
	}
	state["serial"] = json.Number(fmt.Sprint(n + 1))
	return folded, nil
}

func instanceKey(module, mode, typ, name, index any) string {
	k, _ := json.Marshal([]any{emptyIfNil(module), mode, typ, name, index})
	return string(k)
}

func emptyIfNil(v any) any {
	if v == nil {
		return ""
	}
	return v
}

func quietActions(a any) bool {
	actions, _ := a.([]any)
	return len(actions) == 1 && (actions[0] == "no-op" || actions[0] == "read")
}

// merge takes the plan's value wherever the plan knows it, and keeps the
// record's wherever it does not.
//
// A value the plan cannot know is left out of "after" and marked in
// after_unknown. Dropping it would make the next plan see it as removed, so
// it keeps what the record already holds.
func merge(old, after, unknown any) any {
	if unknown == true {
		return old
	}
	switch a := after.(type) {
	case map[string]any:
		o, _ := old.(map[string]any)
		u, _ := unknown.(map[string]any)
		out := make(map[string]any, len(a))
		for k, v := range a {
			out[k] = merge(o[k], v, u[k])
		}
		for k, uu := range u {
			if _, present := a[k]; !present {
				out[k] = merge(o[k], nil, uu)
			}
		}
		// A dynamic-typed attribute is stored as {"value", "type"} in state
		// but planned as the bare value; the wrapper is the record's, and
		// it is kept.
		for k, v := range o {
			if w, ok := v.(map[string]any); ok && isDynamic(w) {
				if nv, ok := out[k]; ok {
					if nw, ok := nv.(map[string]any); !ok || !isDynamic(nw) {
						out[k] = map[string]any{"value": nv, "type": w["type"]}
					}
				}
			}
		}
		return out
	case []any:
		o, _ := old.([]any)
		u, _ := unknown.([]any)
		out := make([]any, len(a))
		for i, v := range a {
			var ov, uv any
			if i < len(o) {
				ov = o[i]
			}
			if i < len(u) {
				uv = u[i]
			}
			out[i] = merge(ov, v, uv)
		}
		return out
	case nil:
		if _, nested := unknown.(map[string]any); nested {
			return merge(old, map[string]any{}, unknown)
		}
		if _, nested := unknown.([]any); nested {
			return old
		}
		return nil
	default:
		return after
	}
}

func isDynamic(m map[string]any) bool {
	_, v := m["value"]
	_, t := m["type"]
	return v && t && len(m) == 2
}

// sensitivePaths turns a plan's after_sensitive tree into the path list a
// state file records. A plan compares sensitivity as well as value, so a value
// whose marks differ from the record's is planned as an update even when it is
// identical.
func sensitivePaths(node any, path []any, acc []any) []any {
	switch t := node.(type) {
	case bool:
		if t && len(path) > 0 {
			acc = append(acc, append([]any{}, path...))
		}
	case map[string]any:
		for k, v := range t {
			acc = sensitivePaths(v, append(path, map[string]any{"type": "get_attr", "value": k}), acc)
		}
	case []any:
		for i, v := range t {
			step := map[string]any{"type": "index", "value": map[string]any{"value": json.Number(fmt.Sprint(i)), "type": "number"}}
			acc = sensitivePaths(v, append(path, step), acc)
		}
	}
	return acc
}
