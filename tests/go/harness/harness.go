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
	"os"
	"path/filepath"
	"slices"
	"testing"

	"homelab/contractor/config"
	"homelab/details/repopath"
)

// RepoRoot is repopath.Root with the test's failure attached.
//
// It used to count its own way up with a fixed chain of "..", one of eight
// copies of that answer across two modules. Each was right until the file
// holding it moved.
func RepoRoot(t *testing.T) string {
	t.Helper()
	root, err := repopath.Root()
	if err != nil {
		t.Fatal(err)
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

// The config types are the program's own, aliased rather than copied.
//
// This used to be a separate declaration, defended in a comment as an
// independent reader: "a test that parsed the file with the same code as the
// program under test would agree with that program about a misreading". It
// caught no misreading. When the object-storage block changed shape, the
// program's reader was updated and this copy was not; Go's decoder filled the
// missing fields with empty strings, and the nightly handed object storage an
// empty bucket name and empty credentials while its message blamed the vault.
//
// The copy was never a choice so much as a consequence: the program kept its
// config under internal/, which Go forbids another module to import. It now
// lives in homelab/contractor/config, and these tiers read the rendered config
// with the same reader the program uses - one building block, so a change to
// it reaches every consumer at once.
type (
	Config  = config.Config
	Site_   = config.Site
	Tunnel_ = config.Tunnel
)

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
	// Stat, not read: this only decides which message a missing file gets.
	// Reading it is LoadRendered's job, so there is one reader.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf(`no rendered config at %s.

These tiers read the same file the start button reads, so rendering it is the
setup step:

    task render-secrets SITE=%s

and wiping it again is the teardown:

    task clean-secrets SITE=%s

underlying error: %v`, path, Site(), Site(), err)
	}
	cfg, err := config.LoadRendered(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// SiteConfig is LoadConfig narrowed to the site under test.
// ObjectStorageAccount is the fleet-level half of object storage. Separate
// accessor from SiteConfig because the two live on different planes, and a
// test that reached for the account through a site would be asserting a
// containment the config deliberately does not have.
func ObjectStorageAccount(t *testing.T) config.ObjectStorageAccount {
	t.Helper()
	return LoadConfig(t).ObjectStorage
}

// SiteNetwork is the site under test with its addressing resolved - by the
// program's own ResolveSiteNetwork, so an address a test dials is the address
// the program built.
func SiteNetwork(t *testing.T) *config.SiteNetwork {
	t.Helper()
	net, err := config.ResolveSiteNetwork(LoadConfig(t), Site())
	if err != nil {
		t.Fatal(err)
	}
	return net
}

// Machines is every node this site builds, whatever its role.
//
// It was ControlPlaneCount + WorkerCount, kept here as a second copy of the
// answer config.SiteNetwork.AllMachineIPs already gave - and the copy had
// already fallen behind: it left out untrusted-zone machines, so the first
// zone declared would have failed a healthy cluster exactly as the Health
// phase did in #261 and this tier did in #262. The test beside it was named
// "counts every class" and checked two of the three.
func Machines(t *testing.T) int {
	t.Helper()
	return len(SiteNetwork(t).AllMachineIPs())
}

// StateBackups is where the Backup phase writes the age-encrypted state dumps,
// and the rclone environment that reaches them - both resolved exactly as the
// program resolves them: the state bucket from config.Buckets, its own
// credential rather than any other, and the folder and remote from the same
// declarations the Backup and Restore phases use.
//
// Every one of those used to be restated in the integration tier. When the
// state dumps moved to a bucket of their own, the restatement was left behind,
// and the check that exists to prove backups are healthy reported them broken.
func StateBackups(t *testing.T) config.StateBackups {
	t.Helper()
	loc, err := config.StateBackupLocation(LoadConfig(t), Site())
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// Alerting is where the estate speaks when something goes wrong. Fleet-level,
// for the same reason ObjectStorageAccount is: a second site would report into
// the same place rather than somewhere new.
func Alerting(t *testing.T) config.Alerting {
	t.Helper()
	return LoadConfig(t).Alerting
}

// Tunnel is the fleet-level tunnel block: the vendor and the token that
// manages it. The member list is personal data and no test needs it.
func Tunnel(t *testing.T) Tunnel_ {
	t.Helper()
	return LoadConfig(t).Tunnel
}

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

// ControlPlaneIP is the address of control-plane node i, as the program
// derived it. It used to restate the formula - 10.<octet>.10.<100+i> - which
// is one more copy for the next addressing change to miss.
func ControlPlaneIP(t *testing.T, i int) string {
	t.Helper()
	ips := SiteNetwork(t).NodeIPs
	if i < 0 || i >= len(ips) {
		t.Fatalf("control-plane index %d is outside this site's %d node(s)", i, len(ips))
	}
	return ips[i]
}
