package asbuilt

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// A value shorter than this is only ever replaced where it is the whole of a
// string, never inside a longer one. Replacing "10" wherever it occurs would
// rewrite every address and port in the state; a short vault value that
// survives as a substring is left for the scan, which fails on it.
const minSubstring = 4

// Where a real value came from, as the scan reports it. A vault value's
// source also names its field: "vault:<field>".
const (
	SourceVault          = "vault"
	SourceMachineSecrets = "talos machine secrets"
	SourceSensitive      = "sensitive"
)

// Replacements maps a real value to the stand-in that takes its place in the
// record. The first stand-in registered for a value wins, so the more
// specific source - the throwaway CA, a shaped vault field - is added before
// the general one.
type Replacements struct {
	to map[string]string
	// Where each real value came from, for the scan's report.
	from map[string]string
	// wholeOnly are values replaced, and looked for, only where they are the
	// whole of a string. See AddWhole.
	wholeOnly map[string]bool
}

func NewReplacements() *Replacements {
	return &Replacements{to: map[string]string{}, from: map[string]string{}, wholeOnly: map[string]bool{}}
}

// AddWhole registers a value that is replaced only where it is the whole of
// a string, with no other forms.
//
// For values marked sensitive by where they sit rather than by what they
// are. A secret's data map is sensitive as a whole, so the namespace name and
// username inside it are too - and replacing "database" wherever it occurs
// would rewrite every resource named after it, which the plan then reports
// as a change. A generated secret is always a whole value where it is
// stored, so nothing is lost by matching it only there.
func (r *Replacements) AddWhole(source, raw, standIn string) {
	if raw == "" || raw == standIn {
		return
	}
	if _, seen := r.to[raw]; seen {
		return
	}
	r.put(source, raw, standIn)
	r.wholeOnly[raw] = true
}

// Add registers raw -> standIn, and the forms raw is also stored in: lower
// case, the DNS label the cluster root derives from a name, and base64.
func (r *Replacements) Add(source, raw, standIn string) {
	if raw == "" || raw == standIn {
		return
	}
	r.put(source, raw, standIn)
	if len(raw) < minSubstring {
		return
	}
	r.put(source, strings.ToLower(raw), strings.ToLower(standIn))
	if l := dnsLabel(raw); l != "" {
		r.put(source, l, dnsLabel(standIn))
	}
	r.put(source, base64.StdEncoding.EncodeToString([]byte(raw)), base64.StdEncoding.EncodeToString([]byte(standIn)))
}

func (r *Replacements) put(source, raw, standIn string) {
	if _, seen := r.to[raw]; seen || raw == standIn {
		return
	}
	r.to[raw] = standIn
	r.from[raw] = source
}

// Has reports whether raw already has a stand-in.
func (r *Replacements) Has(raw string) bool { _, ok := r.to[raw]; return ok }

// Secret is a real value the scan looks for.
type Secret struct {
	Source string
	// Whole means it is only a finding where it is the whole of a string.
	Whole bool
}

// Secrets is every real value registered, with where it came from. It is the
// list the scan looks for, and it deliberately includes the forms: a
// base64-encoded address is as much of a leak as the address.
func (r *Replacements) Secrets() map[string]Secret {
	out := make(map[string]Secret, len(r.from))
	for k, v := range r.from {
		out[k] = Secret{Source: v, Whole: r.wholeOnly[k]}
	}
	return out
}

// replacer is built once per pass: longest first, so a value that contains
// another is replaced whole rather than piecemeal.
func (r *Replacements) replacer() func(string) string {
	keys := make([]string, 0, len(r.to))
	for k := range r.to {
		if len(k) >= minSubstring && !r.wholeOnly[k] {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})
	pairs := make([]string, 0, 2*len(keys))
	for _, k := range keys {
		pairs = append(pairs, k, r.to[k])
	}
	sub := strings.NewReplacer(pairs...)
	return func(s string) string {
		if exact, ok := r.to[s]; ok {
			return exact
		}
		return sub.Replace(s)
	}
}

// rewrite replaces every string inside v, keys of objects included - a map's
// keys are operator data as often as its values.
func rewrite(v any, f func(string) string) any {
	switch t := v.(type) {
	case string:
		return f(t)
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = rewrite(x, f)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[f(k)] = rewrite(x, f)
		}
		return out
	default:
		return v
	}
}

// vaultReference marks a template leaf the vault fills in.
var vaultReference = regexp.MustCompile(`\{\{\s*op://`)

// attestations are vault-sourced but public vocabulary: which vendor the
// vault says a credential belongs to, compared by preconditions against the
// provider the code was written for. Fingerprinting them would fail those
// preconditions and hide nothing.
var attestations = map[string]bool{"provider": true, "vault_provider": true}

// Vault fingerprints every value the template says came from the vault, and
// returns the rendered config with those values replaced.
//
// It walks the template beside the rendered config rather than trusting the
// rendered config to say which values are secret. The template is the
// declaration; a value is a vault value because the template put a reference
// there, not because it looks like one.
func Vault(f *Fingerprinter, r *Replacements, template, rendered any) any {
	return vaultWalk(f, r, "", template, rendered)
}

func vaultWalk(f *Fingerprinter, r *Replacements, field string, tpl, cfg any) any {
	switch t := tpl.(type) {
	case map[string]any:
		c, ok := cfg.(map[string]any)
		if !ok {
			return cfg
		}
		out := make(map[string]any, len(c))
		for k, v := range c {
			if sub, declared := t[k]; declared {
				out[k] = vaultWalk(f, r, k, sub, v)
			} else {
				out[k] = v
			}
		}
		return out
	case string:
		v, ok := cfg.(string)
		if !ok || !vaultReference.MatchString(t) || attestations[field] {
			return cfg
		}
		standIn := f.Field(field, v)
		r.Add(SourceVault+":"+field, v, standIn)
		return standIn
	default:
		return cfg
	}
}

// decode parses JSON keeping numbers as written. A state file round-tripped
// through float64 loses the low digits of a large id, and the plan then
// reports the resource as changed for a reason nobody can see.
func decode(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var v map[string]any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// Decode is decode for callers outside the package.
func Decode(raw []byte) (map[string]any, error) { return decode(raw) }
