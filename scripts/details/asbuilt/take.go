package asbuilt

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"homelab/details/platform"
)

// Tofu runs tofu in dir and hands back both streams, so nothing it prints
// reaches a log unread. A nil env means the caller's own environment: the
// real root is planned with whatever the caller established to read its
// state. Injected so the sequence is testable with canned plans.
type Tofu func(dir string, env []string, args ...string) (stdout, stderr []byte, err error)

// Inputs is what taking a record needs.
type Inputs struct {
	// Root is the root to record, initialised and attached to its state. A
	// site has two, and each is recorded on its own.
	Root string
	// MachineSecrets says this root's state holds the Talos machine secrets,
	// which are swapped for a throwaway set. The cluster root's does, and a
	// state said to hold them that does not is refused: it is not the state
	// of a site.
	MachineSecrets bool
	// Vars are inputs the offline plan needs beyond the site and the config,
	// by variable name: for a root configured from another root's outputs,
	// the stand-ins the record of that root holds for them (OutputVars).
	Vars map[string]string
	// Work is where the record is made: an offline copy of Root and a
	// throwaway CA. It must be two levels below the repository, the same
	// depth as Root, so "${path.module}/../../" still reaches the repository.
	// The caller removes it.
	Work string
	// Template and Rendered are the config template and the config rendered
	// from it. The template is what says which values came from the vault.
	Template, Rendered []byte
	Site               string
	// Progress is told what is happening, in words safe to print anywhere.
	Progress func(string)
}

// Result is what came of it. Nothing in it is a value from the estate.
type Result struct {
	// Replaced counts the real values replaced, by where they came from.
	Replaced map[string]int
	Rounds   int
	Quiet    bool
	// LastPlan is the final offline plan in JSON, planned against the record
	// and therefore holding only stand-ins.
	LastPlan []byte
	// Before is what the scan found after replacing and before any plan:
	// a real value the rules missed. After is what it found in the finished
	// record, which adds what the offline plan computed from the record and
	// public code.
	Before, After []Finding
	// KeyedByAValue is each resource, as "<type>.<name>", with an instance
	// keyed by a real value. The record replaces it; the estate's own logs
	// do not, so it is refused rather than recorded.
	KeyedByAValue []string

	// State and Config are the record itself: the state and the rendered
	// config with every real value replaced, as the last offline plan saw
	// them. Only worth keeping when Publishable.
	State, Config map[string]any
}

// Publishable is a record that is quiet, in which nothing real survived the
// replacing, and of an estate that puts no real value in a resource address.
func (r *Result) Publishable() bool {
	return r.Quiet && len(r.Before) == 0 && len(r.KeyedByAValue) == 0
}

// Computed is what the scan found in the finished record that it did not
// find before planning: values the offline plan computed from the record's
// own contents and public code. They cannot carry anything the record did
// not already hold, so they are reported rather than failed on.
func (r *Result) Computed() []Finding {
	seen := map[Finding]bool{}
	for _, f := range r.Before {
		seen[f] = true
	}
	var out []Finding
	for _, f := range r.After {
		if !seen[f] {
			out = append(out, f)
		}
	}
	return out
}

// NotConvergedError is the refusal to record an estate with pending changes.
// It carries the real plan so the caller can summarise it in its own words.
type NotConvergedError struct{ Plan []byte }

func (e *NotConvergedError) Error() string {
	return "the estate does not match its config, so it cannot be recorded as built"
}

// MaxRounds bounds the folding. The spike converged in four; a record that
// needs more is reporting something folding cannot fix, and looping longer
// would only hide which thing it is.
const MaxRounds = 6

// Take makes the as-built record and reports what came of it. It fails when
// something prevented a record being made at all; a record that was made but
// is not publishable is a Result that says so.
func Take(in Inputs, tofu Tofu) (*Result, error) {
	say := in.Progress
	if say == nil {
		say = func(string) {}
	}
	if err := os.MkdirAll(in.Work, 0o700); err != nil {
		return nil, err
	}

	// 1. The estate has to be converged, or folding would write pending
	// changes into the record as though they had been built.
	say("planning against the estate, to prove it is converged")
	realPlan := filepath.Join(in.Work, "real.tfplan")
	if _, stderr, err := tofu(in.Root, nil, "plan", "-input=false", "-no-color", "-out="+realPlan); err != nil {
		return nil, fmt.Errorf("the real plan failed (%v):\n%s", err, ErrorSummary(stderr))
	}
	liveRaw, _, err := tofu(in.Root, nil, "show", "-json", realPlan)
	_ = os.Remove(realPlan)
	if err != nil {
		return nil, fmt.Errorf("reading the real plan back: %w", err)
	}
	live, err := decode(liveRaw)
	if err != nil {
		return nil, fmt.Errorf("the real plan is not JSON: %w", err)
	}
	if !Quiet(live) {
		return nil, &NotConvergedError{Plan: liveRaw}
	}

	stateRaw, _, err := tofu(in.Root, nil, "state", "pull")
	if err != nil {
		return nil, fmt.Errorf("pulling the state: %w", err)
	}
	state, err := decode(stateRaw)
	wipe(stateRaw)
	if err != nil {
		return nil, fmt.Errorf("the state is not JSON: %w", err)
	}

	// 2. Replace everything real.
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	f, err := NewFingerprinter(key)
	if err != nil {
		return nil, err
	}
	r := NewReplacements()

	if in.MachineSecrets {
		say("generating a throwaway CA")
		throwaway, err := throwawayMachineSecrets(in, tofu, talosVersion(state))
		if err != nil {
			return nil, err
		}
		if _, err := SwapMachineSecrets(state, throwaway, r); err != nil {
			return nil, err
		}
	}
	config, err := fingerprintConfig(f, r, in.Template, in.Rendered)
	if err != nil {
		return nil, err
	}
	if err := SensitiveLeaves(f, r, live); err != nil {
		return nil, err
	}
	keyed := Scrub(state, r)
	secrets := r.Secrets()
	res := &Result{Replaced: map[string]int{}, KeyedByAValue: keyed}
	for _, s := range secrets {
		res.Replaced[strings.SplitN(s.Source, ":", 2)[0]]++
	}
	res.Before = Scan(state, secrets, config)

	// 3. Plan offline against the record, folding until it is quiet.
	scratch := filepath.Join(in.Work, "cluster")
	if err := copyRoot(in.Root, scratch); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(scratch, "config.json"), config); err != nil {
		return nil, err
	}
	env := append(OfflineEnv(os.Environ(), in.Site, filepath.Join(scratch, "config.json")), varEnv(in.Vars)...)
	if _, stderr, err := tofu(scratch, env, "init", "-input=false", "-no-color", pluginDir(in.Root)); err != nil {
		return nil, fmt.Errorf("initialising the offline copy (%v):\n%s", err, ErrorSummary(stderr))
	}

	for res.Rounds < MaxRounds && !res.Quiet {
		res.Rounds++
		say(fmt.Sprintf("planning offline against the record, round %d", res.Rounds))
		if err := writeJSON(filepath.Join(scratch, "terraform.tfstate"), state); err != nil {
			return nil, err
		}
		if _, stderr, err := tofu(scratch, env, "plan", "-refresh=false", "-lock=false", "-input=false", "-no-color", "-out=tfplan"); err != nil {
			return nil, fmt.Errorf("the offline plan failed in round %d. A value that needs a shape it was not given fails here, and the fingerprint table grows from these:\n%s",
				res.Rounds, ErrorSummary(stderr)+fmt.Sprintf("\n    (%v)", err))
		}
		if res.LastPlan, _, err = tofu(scratch, env, "show", "-json", "tfplan"); err != nil {
			return nil, fmt.Errorf("reading the offline plan back: %w", err)
		}
		plan, err := decode(res.LastPlan)
		if err != nil {
			return nil, err
		}
		if res.Quiet = Quiet(plan); !res.Quiet {
			if _, err := Fold(state, plan); err != nil {
				return nil, err
			}
		}
	}

	res.After = Scan(state, secrets, config)
	res.State, res.Config = state, config
	return res, nil
}

// Quiet is a plan with nothing to do: every resource a no-op or a read, and
// every output unchanged.
func Quiet(plan map[string]any) bool {
	changes, _ := plan["resource_changes"].([]any)
	for _, c := range changes {
		rc, _ := c.(map[string]any)
		change, _ := rc["change"].(map[string]any)
		if !quietActions(change["actions"]) {
			return false
		}
	}
	outputs, _ := plan["output_changes"].(map[string]any)
	for _, o := range outputs {
		change, _ := o.(map[string]any)
		actions, _ := change["actions"].([]any)
		if len(actions) != 1 || actions[0] != "no-op" {
			return false
		}
	}
	return true
}

// Pending names what a plan would change, as type.name and output.name only:
// never an instance key, which can be a vault value, and never a value.
func Pending(plan []byte) ([]string, error) {
	doc, err := decode(plan)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	changes, _ := doc["resource_changes"].([]any)
	for _, c := range changes {
		rc, _ := c.(map[string]any)
		change, _ := rc["change"].(map[string]any)
		if !quietActions(change["actions"]) {
			seen[fmt.Sprintf("%v.%v", rc["type"], rc["name"])] = true
		}
	}
	outputs, _ := doc["output_changes"].(map[string]any)
	for name, o := range outputs {
		change, _ := o.(map[string]any)
		if actions, _ := change["actions"].([]any); len(actions) != 1 || actions[0] != "no-op" {
			seen["output."+name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// PluginDir is the flag that points an init at a root's own provider cache,
// so a copy of the root plans with the providers it already holds.
func PluginDir(root string) string { return pluginDir(root) }

func pluginDir(root string) string {
	return "-plugin-dir=" + filepath.Join(root, ".terraform", "providers")
}

func talosVersion(state map[string]any) string {
	v := ""
	instances(state, func(res, inst map[string]any) {
		if res["type"] != "talos_machine_secrets" {
			return
		}
		attrs, _ := inst["attributes"].(map[string]any)
		if s, ok := attrs["talos_version"].(string); ok {
			v = s
		}
	})
	return v
}

// versionPattern is what a talos_version may look like before it is written
// into HCL. It comes from the state, and a value spliced into a file is a
// value that could close the string it was put in.
var versionPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+(\.[0-9]+)?$`)

// throwawayMachineSecrets generates a Talos CA and machine secrets that
// nothing trusts, with the provider the estate uses, so they are exactly the
// shape the configs derived from them expect.
func throwawayMachineSecrets(in Inputs, tofu Tofu, version string) (map[string]any, error) {
	dir := filepath.Join(in.Work, "pki")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	arg := ""
	if version != "" {
		if !versionPattern.MatchString(version) {
			return nil, errors.New("the state's talos_version is not a version, so it is not written into a file")
		}
		arg = fmt.Sprintf("  talos_version = %q\n", version)
	}
	main := "terraform {\n  required_providers {\n    talos = { source = \"siderolabs/talos\" }\n  }\n}\n\n" +
		"# A throwaway PKI for the as-built record: the real CA never leaves the estate.\n" +
		"resource \"talos_machine_secrets\" \"throwaway\" {\n" + arg + "}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(main), 0o600); err != nil {
		return nil, err
	}
	env := OfflineEnv(os.Environ(), in.Site, "")
	for _, args := range [][]string{
		{"init", "-input=false", pluginDir(in.Root)},
		{"apply", "-auto-approve", "-input=false"},
	} {
		if _, stderr, err := tofu(dir, env, args...); err != nil {
			return nil, fmt.Errorf("generating the throwaway CA (tofu %s: %v):\n%s", args[0], err, ErrorSummary(stderr))
		}
	}
	raw, _, err := tofu(dir, env, "state", "pull")
	if err != nil {
		return nil, fmt.Errorf("reading the throwaway CA back: %w", err)
	}
	st, err := decode(raw)
	if err != nil {
		return nil, err
	}
	var attrs map[string]any
	instances(st, func(res, inst map[string]any) {
		if res["type"] == "talos_machine_secrets" {
			attrs, _ = inst["attributes"].(map[string]any)
		}
	})
	if attrs == nil {
		return nil, errors.New("the throwaway CA's state holds no machine secrets")
	}
	return attrs, nil
}

func fingerprintConfig(f *Fingerprinter, r *Replacements, template, rendered []byte) (map[string]any, error) {
	var tpl any
	if err := json.Unmarshal(template, &tpl); err != nil {
		return nil, fmt.Errorf("the config template is not JSON: %w", err)
	}
	cfg, err := decode(rendered)
	if err != nil {
		return nil, fmt.Errorf("the rendered config is not JSON: %w", err)
	}
	out, _ := Vault(f, r, tpl, cfg).(map[string]any)
	return out, nil
}

// copyRoot copies the root's configuration: never its provider cache, the
// backend switch, state, a saved plan or the tests. Without backend_pg.tf the
// copy's backend is local, which is what points it at the record.
//
// It refuses a destination at any other depth than the root's. The copy is
// otherwise perfect, and the first sign of the wrong depth is a plan failing
// on a file() call it cannot resolve - which is how it was found.
func copyRoot(from, to string) error {
	if depth(from) != depth(to) {
		return fmt.Errorf("the copy of %s must be two levels below the repository, as the root is, so its \"${path.module}/../../\" references still resolve; %s is not", from, to)
	}
	if err := os.MkdirAll(to, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !(strings.HasSuffix(name, ".tf") || name == ".terraform.lock.hcl") || name == "backend_pg.tf" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(from, name))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(to, name), b, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// varEnv is variables as tofu reads them from the environment, in a stable
// order.
func varEnv(vars map[string]string) []string {
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, "TF_VAR_"+name+"="+vars[name])
	}
	return out
}

// OutputVars reads outputs out of a record's state, as the variables of the
// same names another root takes them as: a string as itself, anything else
// as JSON. Every name must be there. A record without one cannot stand in for
// the root it is of, and planning with the variable unset would fail on
// something that names neither.
func OutputVars(state map[string]any, names ...string) (map[string]string, error) {
	outputs, _ := state["outputs"].(map[string]any)
	vars := map[string]string{}
	for _, name := range names {
		out, _ := outputs[name].(map[string]any)
		value, ok := out["value"]
		if !ok || value == nil {
			return nil, fmt.Errorf("the record holds no %q output, so the root that reads it cannot be planned against this record", name)
		}
		if s, ok := value.(string); ok {
			vars[name] = s
			continue
		}
		b, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		vars[name] = string(b)
	}
	return vars, nil
}

// MergePlans is several roots' plans as one: every resource change and every
// output change, in the order given. A site is planned root by root and
// reported once, because what a reader wants is what the change does to the
// site.
func MergePlans(plans ...[]byte) ([]byte, error) {
	var merged map[string]any
	for _, raw := range plans {
		doc, err := decode(raw)
		if err != nil {
			return nil, fmt.Errorf("a plan is not JSON: %w", err)
		}
		if merged == nil {
			merged = doc
			continue
		}
		for _, list := range []string{"resource_changes", "resource_drift"} {
			more, _ := doc[list].([]any)
			if len(more) == 0 {
				continue
			}
			have, _ := merged[list].([]any)
			merged[list] = append(have, more...)
		}
		if more, _ := doc["output_changes"].(map[string]any); len(more) > 0 {
			have, _ := merged["output_changes"].(map[string]any)
			if have == nil {
				have = map[string]any{}
			}
			for name, change := range more {
				have[name] = change
			}
			merged["output_changes"] = have
		}
	}
	if merged == nil {
		return nil, errors.New("no plan was given, so there is nothing to report")
	}
	return json.Marshal(merged)
}

// OfflineEnv is the environment for a tofu that must reach nothing real.
//
// It owns every namespace a credential could arrive through, not only the
// ones it sets: an inherited TF_ENCRYPTION would make the record's plain state
// unreadable, and an inherited provider variable - a Proxmox endpoint, a
// kubeconfig path - is a real credential a provider would use in preference to
// the stand-in it was configured with.
func OfflineEnv(base []string, site, configPath string) []string {
	owned := []string{"TF_", "PROXMOX_", "TAILSCALE_", "KUBE", "TALOS", "CLOUDFLARE_", "AWS_", "OP_"}
	out := make([]string, 0, len(base)+7)
	for _, kv := range base {
		drop := false
		for _, p := range owned {
			if strings.HasPrefix(kv, p) {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	out = append(out, "TF_IN_AUTOMATION=1", "TF_VAR_offline=true", "TF_VAR_site="+site)
	// And the release of the platform the site runs, which is the caller's
	// to have chosen: where it is published, which one, and the settings
	// that let tofu fetch it. The one thing here that is inherited, by name;
	// a record is of an estate, and an estate runs its release.
	for _, kv := range base {
		for _, name := range []string{platform.ReleaseVariable, platform.DigestVariable, platform.CLIConfigVariable} {
			if strings.HasPrefix(kv, name+"=") {
				out = append(out, kv)
			}
		}
	}
	if configPath != "" {
		out = append(out, "TF_VAR_config_path="+configPath)
	}
	return out
}

// diagnostic is the part of a tofu error that names what failed and where.
// The detail beneath it can quote the value that caused it, so it is not
// kept: this output is meant to be pasted.
var diagnostic = regexp.MustCompile(`(Error: .*|on [^ ]+\.tf line [0-9]+.*)$`)

// ErrorSummary keeps only the diagnostic lines of tofu's stderr.
func ErrorSummary(stderr []byte) string {
	var keep []string
	for _, line := range strings.Split(string(stderr), "\n") {
		if m := diagnostic.FindString(line); m != "" {
			keep = append(keep, "    "+m)
		}
	}
	if len(keep) == 0 {
		return "    (tofu gave no diagnostic)"
	}
	return strings.Join(keep, "\n")
}

// depth counts a path's separators, which is all copyRoot needs: whether two
// directories sit at the same depth, not where the repository is.
func depth(p string) int {
	return strings.Count(filepath.ToSlash(filepath.Clean(p)), "/")
}

// Exec is the Tofu every caller outside a test uses: tofu, run in dir, with
// both streams captured. Captured because what tofu prints can name an address
// whose for_each key is a vault value, and the callers decide what reaches a
// log - a diagnostic line through ErrorSummary, never the detail. A nil env is
// the caller's own.
func Exec(dir string, env []string, args ...string) ([]byte, []byte, error) {
	// proved by TestEveryProgramRunIsNamedByAConstantOrIsTheOperators
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	c := exec.Command("tofu", args...)
	c.Dir = dir
	if env != nil {
		c.Env = env
	}
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	err := c.Run()
	return out.Bytes(), errb.Bytes(), err
}

func writeJSON(path string, v any) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	return os.WriteFile(path, b.Bytes(), 0o600)
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
