package repo

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The placeholder config renderer, run for real.
//
// WHY. `tofu validate` and the Validate lane both read
// config/management.placeholder.json, and this script is the only thing that
// writes it. If it produced an empty or malformed file, tofu would fail while
// evaluating a configuration block and name a provider rather than this script
// - so the failure would be loud, confusing, and pointed at the wrong file.
//
// It was on tests/coverage-blocklist.yml as #288: shipped, depended on by two
// lanes, and executed by no test. Reading its source cannot answer the only
// question that matters, which is what comes out the other end.
//
// The two `grep` refusals at the end are the interesting half. They are the
// backstop for a template marker that survived substitution and for a bare
// op:// reference written without the braces - and a refusal nothing exercises
// is indistinguishable from one that cannot fire.
//
// covers: shell:.github/scripts/placeholder-config.sh

// runPlaceholderConfig copies the template into a throwaway tree, runs the real
// script there, and returns what it wrote.
//
// Copied rather than run in place because the script writes into config/, and a
// test that overwrites a file the developer's own `task validate` is reading is
// the exact collision the script's own header describes.
func runPlaceholderConfig(t *testing.T, template string) (stdout string, body []byte, err error) {
	t.Helper()
	root := repoRoot(t)
	dir := t.TempDir()

	if mkErr := os.MkdirAll(filepath.Join(dir, "config"), 0o755); mkErr != nil {
		t.Fatalf("creating config/: %v", mkErr)
	}
	if wErr := os.WriteFile(filepath.Join(dir, "config", "management.tpl.json"), []byte(template), 0o644); wErr != nil {
		t.Fatalf("writing the template: %v", wErr)
	}

	cmd := exec.Command("bash", root+"/.github/scripts/placeholder-config.sh")
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	out, runErr := cmd.CombinedOutput()

	body, readErr := os.ReadFile(filepath.Join(dir, "config", "management.placeholder.json"))
	if readErr != nil {
		body = nil
	}
	return string(out), body, runErr
}

// The real template renders to valid JSON carrying every key it declared.
//
// "Valid JSON" alone would pass on `{}`, which is why the key comparison is
// here: the failure this guards is a substitution that ate a line, not one that
// produced a syntax error.
func TestThePlaceholderConfigRendersTheRealTemplate(t *testing.T) {
	template := readRepoFile(t, "config/management.tpl.json")

	stdout, body, err := runPlaceholderConfig(t, template)
	if err != nil {
		t.Fatalf("the script refused the template that ships in this repository: %v\n%s", err, stdout)
	}
	if len(body) == 0 {
		t.Fatal("the script exited 0 and wrote nothing. `tofu validate` would then fail " +
			"inside a provider rather than here, naming neither this script nor the file " +
			"it was supposed to write.")
	}

	var rendered, source any
	if err := json.Unmarshal(body, &rendered); err != nil {
		t.Fatalf("the rendered config is not valid JSON: %v\n\n%s", err, body)
	}

	// The template is not JSON until it is substituted, so it is compared by
	// rendering it a second way: every key must survive.
	if err := json.Unmarshal([]byte(placeholderise(template)), &source); err != nil {
		t.Fatalf("the fixture substitution produced invalid JSON, so this test is "+
			"comparing against nothing: %v", err)
	}

	missing := missingKeys(t, source, rendered, "")
	if len(missing) > 0 {
		t.Errorf(`the rendered config has lost %d key(s) the template declares:

  %s

Every one of these is read by management/cluster, so validate would fail on a
missing attribute and name the HCL rather than the substitution that dropped it.`,
			len(missing), strings.Join(missing, "\n  "))
	}
}

// A marker that survived substitution is refused rather than written.
//
// The script's own sweep replaces `{{ ... }}`, so reaching this refusal takes a
// marker the sweep cannot match. A stray `{{` with no closing braces anywhere
// after it is that case, and it is what a half-finished edit looks like.
func TestThePlaceholderConfigRefusesAnUnresolvedMarker(t *testing.T) {
	stdout, _, err := runPlaceholderConfig(t, `{"organization": {"name": "{{ broken"}`)
	if err == nil {
		t.Fatal("the script accepted a template with an unresolved marker. That file " +
			"reaches tofu as a literal `{{`, which is a value no provider recognises " +
			"and no error names this script.")
	}
	if !strings.Contains(stdout, "unresolved template markers") {
		t.Errorf("the script refused but did not say why:\n%s", stdout)
	}
}

// A substitution that ran past the end of its marker is refused.
//
// This is the case #288 was actually about, and the one every other check here
// misses. `{{ broken" }}` on a single-line template has no closing braces of
// its own, so sed matches from the marker to the JSON's own `}}` and eats the
// structure in between. Nothing is left to grep for: no marker survives, no
// op:// survives, and before this check the script exited 0 having written
//
//	{"organization": {"name": "placeholder
//
// which `tofu validate` then fails on from inside a provider configuration
// block, naming neither this script nor the template.
func TestThePlaceholderConfigRefusesOutputThatIsNotJSON(t *testing.T) {
	stdout, body, err := runPlaceholderConfig(t, `{"organization": {"name": "{{ broken" }}`)
	if err == nil {
		t.Fatalf(`the script exited 0 having written:

%s
That is not JSON. It reaches tofu through jsondecode(file(...)), so the error
arrives from a provider rather than from here - which is the confusing failure
this check exists to replace with a clear one.`, body)
	}
	if !strings.Contains(stdout, "not valid JSON") {
		t.Errorf("the script refused but did not say the output was not JSON:\n%s", stdout)
	}
}

// A bare op:// reference is refused.
//
// `op inject` substitutes nothing that is not wrapped in braces, so an unwrapped
// reference is valid JSON that reaches whatever reads the key as the literal
// string "op://homelab/...". This is the backstop for the hermetic version of
// the same check, and it had never been run.
func TestThePlaceholderConfigRefusesAnUnwrappedVaultReference(t *testing.T) {
	stdout, _, err := runPlaceholderConfig(t, `{"organization": {"name": "op://homelab/organization/name"}}`)
	if err == nil {
		t.Fatal("the script accepted a bare op:// reference. It survives every schema " +
			"check after it, and the literal reference string reaches whatever reads " +
			"that key.")
	}
	if !strings.Contains(stdout, "unwrapped op://") {
		t.Errorf("the script refused but did not say why:\n%s", stdout)
	}
}

// URL-shaped keys and the private key come out in a shape a provider accepts.
//
// These are the two special cases the script's first pass exists for, and both
// were added because a provider rejected the generic placeholder while validate
// was still evaluating its configuration block - the flux provider checks the
// repository URL has a scheme, and private_key reaches a Kubernetes secret's
// binary_data, which must be valid base64.
func TestThePlaceholderConfigKeepsTheShapeProvidersCheck(t *testing.T) {
	_, body, err := runPlaceholderConfig(t, `{
  "source_control": {
    "repo_url": "{{ op://homelab/source-control/url }}",
    "foreman_bot": { "private_key": "{{ op://homelab/source-control/foreman-bot/private_key }}" }
  },
  "database": { "endpoint": "{{ op://homelab/db/endpoint }}" }
}`)
	if err != nil {
		t.Fatalf("the script refused a well-formed template: %v", err)
	}

	var got struct {
		SourceControl struct {
			RepoURL    string `json:"repo_url"`
			ForemanBot struct {
				PrivateKey string `json:"private_key"`
			} `json:"foreman_bot"`
		} `json:"source_control"`
		Database struct {
			Endpoint string `json:"endpoint"`
		} `json:"database"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("the rendered config is not valid JSON: %v\n%s", err, body)
	}

	for _, tc := range []struct{ name, value, wantPrefix string }{
		{"source_control.repo_url", got.SourceControl.RepoURL, "https://"},
		{"database.endpoint", got.Database.Endpoint, "https://"},
	} {
		if !strings.HasPrefix(tc.value, tc.wantPrefix) {
			t.Errorf("%s rendered as %q, which has no scheme. The flux provider checks "+
				"for one while validate is still evaluating its configuration block, so "+
				"this fails before any resource is read.", tc.name, tc.value)
		}
	}
	if got.SourceControl.ForemanBot.PrivateKey == "placeholder" {
		t.Error(`private_key rendered as the literal "placeholder", which is not valid
base64. It reaches a Kubernetes secret's binary_data, and the decode fails
there rather than here.`)
	}
}

// placeholderise substitutes the template the crude way, so the test has
// something valid to compare key sets against without depending on the script
// it is testing.
func placeholderise(template string) string {
	out := template
	for {
		start := strings.Index(out, "{{")
		if start < 0 {
			return out
		}
		end := strings.Index(out[start:], "}}")
		if end < 0 {
			return out
		}
		out = out[:start] + "placeholder" + out[start+end+2:]
	}
}

// missingKeys reports every path present in want and absent from got.
func missingKeys(t *testing.T, want, got any, path string) []string {
	t.Helper()
	wantMap, ok := want.(map[string]any)
	if !ok {
		return nil
	}
	gotMap, ok := got.(map[string]any)
	if !ok {
		return []string{path}
	}
	var missing []string
	for k, v := range wantMap {
		child := k
		if path != "" {
			child = path + "." + k
		}
		g, present := gotMap[k]
		if !present {
			missing = append(missing, child)
			continue
		}
		missing = append(missing, missingKeys(t, v, g, child)...)
	}
	return missing
}
