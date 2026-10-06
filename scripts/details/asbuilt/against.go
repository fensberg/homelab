package asbuilt

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// NothingPlanned is what tofu shows for a plan that changes nothing. For a
// stand-in tofu to answer with: the tests here and the contractor's both
// need one, and it is tofu's shape, not either's.
const NothingPlanned = `{"format_version": "1.2", "resource_changes": []}`

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
	// Sequence is the converge's steps, planned in order (#497).
	Sequence []PlanStep
	// PluginDir, when set, is a provider cache to plan from, so a machine
	// that already holds the providers does not fetch them again. A hosted
	// runner leaves it empty and fetches them from the registry.
	PluginDir string
	// Vars are inputs the plan needs beyond the site and the config, by
	// variable name (see Inputs.Vars).
	Vars map[string]string
}

// PlanAgainst plans a change against a record, offline, the way a converge
// would apply it - its steps in order - and returns the whole plan in JSON with
// the record's metadata.
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
	env := append(OfflineEnv(os.Environ(), in.Site, filepath.Join(dir, configFile)), varEnv(in.Vars)...)
	init := []string{"init", "-input=false", "-no-color"}
	if in.PluginDir != "" {
		init = append(init, "-plugin-dir="+in.PluginDir)
	}
	if _, stderr, err := tofu(dir, env, init...); err != nil {
		return nil, meta, fmt.Errorf("initialising the plan (%v):\n%s", err, ErrorSummary(stderr))
	}
	plan, err := PlanSteps(StepsInputs{Dir: dir, Env: env, Sequence: in.Sequence}, tofu)
	if err != nil {
		return nil, meta, err
	}
	keyed, err := KeyedByAVaultValue(plan, tpl, config)
	if err != nil {
		return nil, meta, err
	}
	if len(keyed) > 0 {
		return nil, meta, &VaultKeyError{Keyed: keyed}
	}
	return plan, meta, nil
}

// WriteUnbuilt writes the record of a root nothing has been built from: no
// resources, and no config. A change planned against it is planned as the
// first build would apply it, with a stand-in for every vault value - which
// is how a change is checked before any estate has a record of the right
// shape to plan it against.
func WriteUnbuilt(dir, site string) error {
	lineage := make([]byte, 16)
	if _, err := rand.Read(lineage); err != nil {
		return err
	}
	return Write(dir, &Result{
		Quiet: true,
		State: map[string]any{
			"version": json.Number("4"), "serial": json.Number("1"),
			"lineage":   fmt.Sprintf("%x-%x-%x-%x-%x", lineage[0:4], lineage[4:6], lineage[6:8], lineage[8:10], lineage[10:16]),
			"outputs":   map[string]any{},
			"resources": []any{},
		},
		Config: map[string]any{},
	}, Meta{Site: site})
}

// UnbuiltAccess is stand-ins for a root's inputs that are another root's
// outputs, for planning it before that other root has a record: for each
// name, a value of the shape the cluster's access has. The structured form
// carries a certificate and a key that match each other and that nothing
// trusts; the plain form is an opaque string, which is all a kubeconfig is to
// a plan that runs no provisioner.
func UnbuiltAccess(structured, plain string) map[string]string {
	cert := base64.StdEncoding.EncodeToString([]byte(throwawayPEM("certificate")))
	key := base64.StdEncoding.EncodeToString([]byte(throwawayPEM("key")))
	access, _ := json.Marshal(map[string]string{
		"host":               "https://198.18.0.1:6443",
		"ca_certificate":     cert,
		"client_certificate": cert,
		"client_key":         key,
	})
	return map[string]string{structured: string(access), plain: "unbuilt"}
}

// VaultKeyError is the refusal of a change that keys a resource by a value
// from the vault. It names the resource and the config field, both of which
// are public: the type and name are code, and the field is in the template.
type VaultKeyError struct{ Keyed []string }

func (e *VaultKeyError) Error() string {
	return "this change keys a resource by a value from the vault. A resource's key is part of its address, and every plan, apply and log that names the resource prints its address:\n\n    " +
		strings.Join(e.Keyed, "\n    ") +
		"\n\nKey it by a key the config declares (a site or node key, a number) and read the vault's value as an attribute."
}

// KeyedByAVaultValue is every resource in a plan with an instance key that
// holds a value the template takes from the vault, as "<type>.<name> is keyed
// by <config field>".
//
// Every vault value, whatever it is: the plan is made with a stand-in for
// each one, so a key that holds a stand-in holds that field's value in the
// estate. It sees what the change would build as well as what is built,
// because a plan has both, and it sees a key however it was arrived at - a
// for_each over a map, a set built in a local, a module - because it reads
// the result rather than the code.
func KeyedByAVaultValue(plan []byte, tpl, config any) ([]string, error) {
	doc, err := decode(plan)
	if err != nil {
		return nil, fmt.Errorf("the plan is not JSON: %w", err)
	}
	standIns := map[string]string{}
	vaultStandIns("", "", tpl, config, standIns)

	found := map[string]bool{}
	changes, _ := doc["resource_changes"].([]any)
	for _, c := range changes {
		change, _ := c.(map[string]any)
		// The whole address, so a module instance keyed by a value is seen
		// as well as a resource's own key.
		address, _ := change["address"].(string)
		index, _ := change["index"].(string)
		for standIn, field := range standIns {
			if strings.Contains(index, standIn) || strings.Contains(address, standIn) {
				found[fmt.Sprintf("%v.%v is keyed by %s", change["type"], change["name"], field)] = true
			}
		}
	}
	out := make([]string, 0, len(found))
	for f := range found {
		out = append(out, f)
	}
	sort.Strings(out)
	return out, nil
}

// vaultStandIns collects the value the config holds for each template leaf
// the vault fills in, against the field's path. Attestations are public
// vocabulary and are not collected; a value too short to be told from
// coincidence is not either.
func vaultStandIns(path, field string, tpl, config any, out map[string]string) {
	switch t := tpl.(type) {
	case map[string]any:
		c, _ := config.(map[string]any)
		for k, v := range t {
			vaultStandIns(strings.TrimPrefix(path+"."+k, "."), k, v, c[k], out)
		}
	case []any:
		c, _ := config.([]any)
		for i, v := range t {
			if i < len(c) {
				vaultStandIns(fmt.Sprintf("%s[%d]", path, i), field, v, c[i], out)
			}
		}
	case string:
		v, ok := config.(string)
		if !ok || !vaultReference.MatchString(t) || attestations[field] || len(v) < minSubstring {
			return
		}
		out[v] = path
	}
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
		// An attestation the record does not hold - a vendor the change
		// adds, or a site not built yet - is not given a stand-in: it is
		// compared against the provider written beside it, and only the
		// same word passes. So it plans as the value the vault has to hold
		// for the config to be accepted at all.
		if ref, ok := t[vaultAttestation].(string); ok && vaultReference.MatchString(ref) {
			if _, recorded := rec[vaultAttestation].(string); !recorded {
				if provider, ok := t[attestedProvider].(string); ok && !vaultReference.MatchString(provider) {
					out[vaultAttestation] = provider
				}
			}
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
