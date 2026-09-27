package asbuilt

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PlanInputs is what planning a change against a record needs. None of it is
// a credential: the record holds nothing real, and the template and root are
// the pull request's own files.
type PlanInputs struct {
	// Root is the cluster root as the change would leave it.
	Root string
	// Work is where the plan is made: the same directory Take is given, one
	// level below the repository, so the copy inside it sits two levels
	// down as the root does. The caller removes it.
	Work string
	// Record is a directory Write produced.
	Record string
	// Template is the config template as the change would leave it.
	Template []byte
	Site     string
	// PluginDir, when set, is a provider cache to plan from, so a machine
	// that already holds the providers does not fetch them again. A hosted
	// runner leaves it empty and fetches them from the registry.
	PluginDir string
}

// PlanAgainst plans a change against a record, offline, and returns the plan
// in JSON with the record's metadata.
//
// The config is the change's template filled in from the record: the change's
// own literals, which are what it proposes, and the record's stand-ins for
// every vault value, which are what the estate was converged with. A vault
// field the change adds has no stand-in in the record yet and is given a new
// one, so it plans as the value it will be once the vault supplies it.
func PlanAgainst(in PlanInputs, tofu Tofu) ([]byte, Meta, error) {
	state, recorded, meta, err := Read(in.Record)
	if err != nil {
		return nil, meta, err
	}
	if meta.Site != "" && meta.Site != in.Site {
		return nil, meta, fmt.Errorf("the record is of %s, not %s", meta.Site, in.Site)
	}
	var tpl any
	if tpl, err = decodeAny(in.Template); err != nil {
		return nil, meta, fmt.Errorf("the config template is not JSON: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, meta, err
	}
	f, err := NewFingerprinter(key)
	if err != nil {
		return nil, meta, err
	}
	config := Reconcile(f, "", tpl, recorded)

	dir := filepath.Join(in.Work, "plan")
	if err := copyRoot(in.Root, dir); err != nil {
		return nil, meta, err
	}
	if err := writeJSON(filepath.Join(dir, configFile), config); err != nil {
		return nil, meta, err
	}
	if err := writeJSON(filepath.Join(dir, stateFile), state); err != nil {
		return nil, meta, err
	}
	env := OfflineEnv(os.Environ(), in.Site, filepath.Join(dir, configFile))
	init := []string{"init", "-input=false", "-no-color"}
	if in.PluginDir != "" {
		init = append(init, "-plugin-dir="+in.PluginDir)
	}
	if _, stderr, err := tofu(dir, env, init...); err != nil {
		return nil, meta, fmt.Errorf("initialising the plan:\n%s", ErrorSummary(stderr))
	}
	if _, stderr, err := tofu(dir, env, "plan", "-refresh=false", "-lock=false", "-input=false", "-no-color", "-out=tfplan"); err != nil {
		return nil, meta, fmt.Errorf("the plan against the record failed:\n%s", ErrorSummary(stderr))
	}
	plan, _, err := tofu(dir, env, "show", "-json", "tfplan")
	if err != nil {
		return nil, meta, fmt.Errorf("reading the plan back: %w", err)
	}
	return plan, meta, nil
}

// Reconcile fills a template from a record's config: a vault reference takes
// the record's stand-in, or a new one where the record has none; anything
// else takes the template's own value, since that is what the change says.
// A key the template no longer has is dropped with it.
func Reconcile(f *Fingerprinter, field string, tpl, recorded any) any {
	switch t := tpl.(type) {
	case map[string]any:
		rec, _ := recorded.(map[string]any)
		out := make(map[string]any, len(t))
		for k, v := range t {
			out[k] = Reconcile(f, k, v, rec[k])
		}
		return out
	case []any:
		rec, _ := recorded.([]any)
		out := make([]any, len(t))
		for i, v := range t {
			var r any
			if i < len(rec) {
				r = rec[i]
			}
			out[i] = Reconcile(f, field, v, r)
		}
		return out
	case string:
		if !vaultReference.MatchString(t) {
			return t
		}
		if s, ok := recorded.(string); ok {
			return s
		}
		// New in this change. The reference itself is the only thing known
		// about it, and it is public: it is in the template.
		return f.Field(field, strings.TrimSpace(t))
	default:
		return tpl
	}
}

func decodeAny(raw []byte) (any, error) {
	return decode(raw)
}
