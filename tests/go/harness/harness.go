// Package harness is the shared floor under the integration, api and e2e
// tiers: finding the repository, refusing to run without the inputs a tier
// needs, and reading the same rendered config the start button reads.
//
// # How a tier is selected
//
// Build tags, not environment variables. `go test ./...` compiles none of
// these files, so there is no way to reach real infrastructure by forgetting
// a flag - the code is not in the binary at all. Running a tier is explicit:
//
//	go test -tags=api         ./api/...
//	go test -tags=integration ./integration/...
//	go test -tags=e2e         ./e2e/...
//
// The env guards below are the second gate, and they exist for legibility
// rather than safety: a tier invoked without credentials should say which
// input is missing, not fail somewhere inside an HTTP client.
//
// # Where the inputs come from
//
// From config/management.rendered.json - the same file the Render phase
// writes and the Sterilize phase wipes. Integration tests therefore need no
// secret plumbing of their own: `task render-secrets` is the setup step, and
// `task clean-secrets` is the teardown. Nothing here ever reads 1Password
// directly, and nothing here leaves a secret on disk that ignite would not
// have left there anyway.
package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// RepoRoot walks up from this source file rather than the working directory,
// so it resolves the same whether `go test` ran from the repo root, from
// tests/go, or from a single tier's directory.
func RepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine this source file's location")
	}
	// <root>/tests/go/harness/harness.go
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); err != nil {
		t.Fatalf("computed repo root %s does not look like the repository: %v", root, err)
	}
	return root
}

// Site is which key in the config's sites map the tier under test is aimed
// at. Defaults to site0, matching the start button's own default.
func Site() string {
	if s := os.Getenv("HOMELAB_TEST_SITE"); s != "" {
		return s
	}
	return "site0"
}

// RequireEnv fails the test immediately, naming every missing variable at
// once rather than one per re-run.
func RequireEnv(t *testing.T, names ...string) {
	t.Helper()
	var missing []string
	for _, n := range names {
		if os.Getenv(n) == "" {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("this tier needs %v set, and %v are missing", names, missing)
	}
}

// --- the rendered config ----------------------------------------------------

// Config holds only the fields a test tier actually reads. It is deliberately
// a separate declaration from config.Config in scripts/ignite rather than a
// shared one: a test that parsed the file with the same code as the program
// under test would agree with that program about a misreading, which is
// exactly the class of bug an independent reader catches.
type Config struct {
	Organization struct {
		Name string `json:"name"`
	} `json:"organization"`
	Sites map[string]Site_ `json:"sites"`
}

type Site_ struct {
	Name              string `json:"name"`
	Octet             int    `json:"octet"`
	ControlPlaneCount int    `json:"control_plane_count"`
	Hypervisor        struct {
		Provider    string `json:"provider"`
		TokenID     string `json:"token_id"`
		TokenSecret string `json:"token_secret"`
		Nodes       map[string]struct {
			Hostname string `json:"hostname"`
			IP       string `json:"ip"`
		} `json:"nodes"`
	} `json:"hypervisor"`
	ObjectStorage struct {
		Provider        string `json:"provider"`
		AccountID       string `json:"account_id"`
		AdminToken      string `json:"admin_token"`
		AccessKeyID     string `json:"access_key_id"`
		SecretAccessKey string `json:"secret_access_key"`
		Bucket          string `json:"bucket"`
	} `json:"object_storage"`
	State struct {
		DBPassword string `json:"db_password"`
	} `json:"state"`
}

// RenderedConfigPath is where the Render phase writes, overridable for a test
// run pointed at a second estate.
func RenderedConfigPath(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("HOMELAB_TEST_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(RepoRoot(t), "config", "management.rendered.json")
}

// LoadConfig reads the rendered config, failing with the command that would
// produce it rather than a bare "no such file".
func LoadConfig(t *testing.T) *Config {
	t.Helper()
	path := RenderedConfigPath(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf(`no rendered config at %s.

These tiers read the same file the start button reads, so rendering it is the
setup step:

    task render-secrets SITE=%s

and wiping it again is the teardown:

    task clean-secrets SITE=%s

underlying error: %v`, path, Site(), Site(), err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("rendered config at %s is not valid JSON: %v", path, err)
	}
	return &cfg
}

// SiteConfig is LoadConfig narrowed to the site under test.
func SiteConfig(t *testing.T) Site_ {
	t.Helper()
	cfg := LoadConfig(t)
	site, ok := cfg.Sites[Site()]
	if !ok {
		t.Fatalf("the rendered config has no site %q; HOMELAB_TEST_SITE selects it", Site())
	}
	return site
}

// FirstHypervisor returns the node the phases themselves reach for when they
// need "any node in the cluster" - sorted by key, so it is the same one every
// run and the same one variables.tf picks.
func FirstHypervisor(t *testing.T) (hostname, ip string) {
	t.Helper()
	site := SiteConfig(t)
	keys := make([]string, 0, len(site.Hypervisor.Nodes))
	for k := range site.Hypervisor.Nodes {
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		t.Fatalf("site %q declares no hypervisor nodes", Site())
	}
	slices.Sort(keys)
	n := site.Hypervisor.Nodes[keys[0]]
	return n.Hostname, n.IP
}

// ControlPlaneIP returns the address of control-plane node i, derived the
// same way variables.tf derives it: 10.<octet>.10.<100+i>.
func ControlPlaneIP(t *testing.T, i int) string {
	t.Helper()
	site := SiteConfig(t)
	if i < 0 || i >= site.ControlPlaneCount {
		t.Fatalf("control-plane index %d is outside this site's %d node(s)", i, site.ControlPlaneCount)
	}
	return fmt.Sprintf("10.%d.10.%d", site.Octet, 100+i)
}
