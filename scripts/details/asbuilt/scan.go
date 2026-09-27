package asbuilt

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
)

// Finding is a real value found in a record, reported by where it is and
// where it came from - never by what it is.
type Finding struct {
	// Where is "<type>.<name>.<attribute>" or "output.<name>": public
	// vocabulary only. An instance key can be a vault value, so it is never
	// part of this.
	Where  string
	Source string
}

func (f Finding) String() string { return f.Where + "  (" + f.Source + ")" }

// Scan looks for every real value in the record, and is the check the record
// is not published without.
//
// It is separate from the replacing on purpose. The replacing is a set of
// rules about where secrets live; the scan asks whether any secret is still
// there, wherever it is, so a rule that was wrong or missing shows up here
// rather than in a public file.
//
// The config documents in extra are checked for everything but the values
// that are only secret where they sit: the config is the template filled in
// with stand-ins, so a whole value it shares with a secret's data is a
// literal from the template, which is public by being in git.
func Scan(state map[string]any, secrets map[string]Secret, extra ...any) []Finding {
	found := map[Finding]bool{}
	scan := func(where string, v any, includeWhole bool) {
		leaves(v, func(s string) {
			for secret, meta := range secrets {
				switch {
				case meta.Whole && includeWhole && s == secret,
					!meta.Whole && (s == secret || (len(secret) >= minSubstring && strings.Contains(s, secret))):
					found[Finding{Where: where, Source: meta.Source}] = true
				}
			}
		})
	}
	check := func(where string, v any) { scan(where, v, true) }
	instances(state, func(res, inst map[string]any) {
		base := fmt.Sprintf("%v.%v", res["type"], res["name"])
		if res["mode"] == "data" {
			base = "data." + base
		}
		attrs, _ := inst["attributes"].(map[string]any)
		for name, v := range attrs {
			check(base+"."+name, v)
		}
		check(base+" (instance key)", inst["index_key"])
		if p, ok := inst["private"].(string); ok {
			if raw, err := base64.StdEncoding.DecodeString(p); err == nil {
				check(base+" (provider private data)", string(raw))
			}
		}
	})
	outputs, _ := state["outputs"].(map[string]any)
	for name, o := range outputs {
		out, _ := o.(map[string]any)
		check("output."+name, out["value"])
	}
	for i, v := range extra {
		scan(fmt.Sprintf("config document %d", i+1), v, false)
	}

	out := make([]Finding, 0, len(found))
	for f := range found {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}
