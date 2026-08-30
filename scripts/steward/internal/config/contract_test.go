package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"homelab/steward/internal/tfsource"
)

// The config contract is implemented twice on purpose: once here in Go, so
// the start button refuses a bad config in milliseconds, and once in
// management/cluster/registry.tf, so `tofu plan` refuses it too even when the
// button is bypassed. Defence in depth is only defence while both halves say
// the same thing, and nothing about the language forces that - so these tests
// do.
//
// They check three different kinds of agreement:
//
//  1. The numbers match. Octet bounds and the vendor map are read straight
//     back out of the OpenTofu source and compared to the Go constants.
//  2. The verdicts match. Every fixture in the shared corpus is run through
//     the Go validator and checked against the verdict recorded in
//     manifest.json - the same verdict registry.tftest.hcl asserts for the
//     HCL side.
//  3. The corpus itself matches. Every case is present on both sides, so a
//     fixture added to one and forgotten on the other fails the build instead
//     of quietly halving its own coverage.
//
// All of this is ordinary `go test`: no tofu binary, no network, no
// credentials. The HCL side runs separately as `tofu test`.

// repoRoot walks up from this source file rather than the working directory,
// so the path holds regardless of where `go test` was invoked from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine this source file's location")
	}
	// <root>/scripts/steward/internal/config/contract_test.go
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); err != nil {
		t.Fatalf("computed repo root %s does not look like the repository: %v", root, err)
	}
	return root
}

func clusterPath(t *testing.T, parts ...string) string {
	t.Helper()
	return filepath.Join(append([]string{repoRoot(t), "management", "cluster"}, parts...)...)
}

func readTF(t *testing.T, name string) string {
	t.Helper()
	src, err := tfsource.Read(clusterPath(t, name))
	if err != nil {
		t.Fatalf("reading the OpenTofu source: %v", err)
	}
	return src
}

// --- 1. the numbers match ---------------------------------------------------

func TestContract_OctetBoundsMatchTheOpenTofuSource(t *testing.T) {
	src := readTF(t, "registry.tf")

	for _, tc := range []struct {
		local string
		go_   int
	}{
		{"octet_min", OctetMin},
		{"octet_max", OctetMax},
	} {
		hcl, err := tfsource.Int(src, tc.local)
		if err != nil {
			t.Fatalf("registry.tf: %v\n\nIf that local was renamed or restructured, this contract needs re-examining, not re-pointing.", err)
		}
		if hcl != tc.go_ {
			t.Errorf("octet bound %s: registry.tf says %d, config.go says %d.\n\nBoth gate a real deployment. Widening one and not the other means the start button and `tofu plan` disagree about which networks are legal.", tc.local, hcl, tc.go_)
		}
	}
}

func TestContract_RequiredProvidersMatchTheOpenTofuSource(t *testing.T) {
	src := readTF(t, "registry.tf")

	hcl, err := tfsource.Map(src, "required_providers_by_concern")
	if err != nil {
		t.Fatalf("registry.tf: %v", err)
	}

	for _, concern := range slices.Sorted(slices.Values(append(keysOf(hcl), keysOf(RequiredProvidersByConcern)...))) {
		want, inHCL := hcl[concern]
		got, inGo := RequiredProvidersByConcern[concern]
		switch {
		case !inGo:
			t.Errorf("concern %q is asserted in registry.tf (%s) but absent from config.RequiredProvidersByConcern - the Go path would accept any vendor for it", concern, want)
		case !inHCL:
			t.Errorf("concern %q is asserted in config.go (%s) but absent from registry.tf's required_providers_by_concern - `tofu plan` would accept any vendor for it", concern, got)
		case want != got:
			t.Errorf("concern %q: registry.tf requires %q, config.go requires %q", concern, want, got)
		}
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// --- the shared corpus ------------------------------------------------------

type corpusCase struct {
	Name          string `json:"name"`
	Fixture       string `json:"fixture"`
	Site          string `json:"site"`
	Valid         bool   `json:"valid"`
	ErrorContains string `json:"go_error_contains"`
	Because       string `json:"because"`
}

func loadCorpus(t *testing.T) []corpusCase {
	t.Helper()
	path := clusterPath(t, "tests", "fixtures", "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the corpus manifest: %v", err)
	}
	var manifest struct {
		Cases []corpusCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	if len(manifest.Cases) == 0 {
		t.Fatalf("%s declares no cases", path)
	}
	return manifest.Cases
}

// --- 2. the verdicts match --------------------------------------------------

func TestContract_CorpusVerdictsMatchTheManifest(t *testing.T) {
	for _, tc := range loadCorpus(t) {
		t.Run(tc.Name, func(t *testing.T) {
			path := clusterPath(t, "tests", "fixtures", tc.Fixture)
			cfg, err := LoadRendered(path)
			if err != nil {
				t.Fatalf("loading fixture %s: %v", tc.Fixture, err)
			}

			_, err = ResolveSiteNetwork(cfg, tc.Site)

			switch {
			case tc.Valid && err != nil:
				t.Errorf("Go rejected a fixture the corpus calls valid: %v\n\nregistry.tftest.hcl expects this case to plan cleanly, so the two implementations now disagree.\nWhy this fixture exists: %s", err, tc.Because)
			case !tc.Valid && err == nil:
				t.Errorf("Go accepted a fixture the corpus calls invalid.\n\nregistry.tftest.hcl expects the HCL side to fail this case, so the two implementations now disagree - and the Go path is the permissive one.\nWhy this fixture exists: %s", tc.Because)
			case !tc.Valid && tc.ErrorContains != "" && !strings.Contains(err.Error(), tc.ErrorContains):
				t.Errorf("Go rejected the fixture, but for the wrong reason.\n  want the message to contain: %q\n  got: %v\n\nA fixture that fails on an unrelated rule is not testing the rule it was written for.", tc.ErrorContains, err)
			}
		})
	}
}

// --- 3. the corpus itself matches -------------------------------------------

func runBlockNames(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(clusterPath(t, "tests", "registry.tftest.hcl"))
	if err != nil {
		t.Fatalf("reading registry.tftest.hcl: %v", err)
	}
	var names []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		if after, ok := strings.CutPrefix(line, `run "`); ok {
			if name, _, found := strings.Cut(after, `"`); found {
				names = append(names, name)
			}
		}
	}
	if len(names) == 0 {
		t.Fatal("registry.tftest.hcl declares no run blocks")
	}
	return names
}

func TestContract_EveryCaseExistsOnBothSides(t *testing.T) {
	hclNames := runBlockNames(t)
	var goNames []string
	for _, tc := range loadCorpus(t) {
		goNames = append(goNames, tc.Name)
	}

	for _, name := range goNames {
		if !slices.Contains(hclNames, name) {
			t.Errorf("corpus case %q has no matching `run` block in registry.tftest.hcl - the HCL implementation is not being tested against this input at all.\n\nAdd:\n\n  run %q {\n    command = plan\n    ...\n  }", name, name)
		}
	}
	for _, name := range hclNames {
		if !slices.Contains(goNames, name) {
			t.Errorf("registry.tftest.hcl declares `run %q` with no matching case in tests/fixtures/manifest.json - the Go implementation is not being tested against this input at all.", name)
		}
	}
}

func TestContract_EveryFixtureFileIsClaimedByACase(t *testing.T) {
	dir := clusterPath(t, "tests", "fixtures")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the fixtures directory: %v", err)
	}

	claimed := map[string]bool{}
	for _, tc := range loadCorpus(t) {
		claimed[tc.Fixture] = true
	}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || name == "manifest.json" {
			continue
		}
		if !claimed[name] {
			t.Errorf("fixture %s is not referenced by any case in manifest.json - it is dead weight, or a case someone forgot to finish wiring up", name)
		}
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("fixture %s: %v", name, err)
		}
	}

	for fixture := range claimed {
		if _, err := os.Stat(filepath.Join(dir, fixture)); err != nil {
			t.Errorf("manifest.json references fixture %s, which does not exist: %v", fixture, err)
		}
	}
}

// --- the real template ------------------------------------------------------

// The fixtures prove the rules work. This proves the file an actual
// deployment reads still satisfies them - specifically that it has not come
// to declare a vendor this code does not implement, which no fixture would
// ever catch because fixtures are written to match.
func TestContract_ConfigTemplateDeclaresImplementedVendors(t *testing.T) {
	path := filepath.Join(repoRoot(t), "config", "management.tpl.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the config template: %v", err)
	}

	// Parsed loosely: the template is full of unresolved {{ op://... }}
	// markers, but those are all string values, so it is still valid JSON.
	var tpl struct {
		Sites map[string]map[string]json.RawMessage `json:"sites"`
	}
	if err := json.Unmarshal(data, &tpl); err != nil {
		t.Fatalf("the config template is not valid JSON: %v", err)
	}
	if len(tpl.Sites) == 0 {
		t.Fatal("the config template declares no sites")
	}

	for siteKey, site := range tpl.Sites {
		for _, concern := range slices.Sorted(slices.Values(keysOf(RequiredProvidersByConcern))) {
			want := RequiredProvidersByConcern[concern]
			raw, ok := site[concern]
			if !ok {
				t.Errorf("sites.%s has no %q block, but the code requires one", siteKey, concern)
				continue
			}
			var block struct {
				Provider string `json:"provider"`
			}
			if err := json.Unmarshal(raw, &block); err != nil {
				t.Errorf("sites.%s.%s: %v", siteKey, concern, err)
				continue
			}
			if block.Provider != want {
				t.Errorf("sites.%s.%s.provider is %q, but this code implements %q.\n\nThe template is what a real run reads. Change the code before changing the declaration.", siteKey, concern, block.Provider, want)
			}
			// A vendor declaration must never be a vault reference: it is
			// the reviewable-in-git half of the three-way check, and a
			// value nobody can read in a diff cannot serve that purpose.
			if strings.Contains(block.Provider, "op://") {
				t.Errorf("sites.%s.%s.provider is a 1Password reference. It must be a literal in git - that is the whole point of declaring it here as well as in the vault.", siteKey, concern)
			}
		}
	}
}
