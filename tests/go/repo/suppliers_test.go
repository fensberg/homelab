package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The approved suppliers list is the estate's declaration of every outside
// party it may take delivery from. These tests are what make it a rule rather
// than a document.
//
// The incident behind it: an unpinned third-party image was proposed for a
// live cluster, with host networking, on a control-plane node, and nothing in
// this repository would have objected. The image was very likely harmless.
// "Very likely harmless" is the reasoning this estate exists to replace.
//
// Scope, stated because a guard nobody understands the edges of is worse than
// none: this catches what is COMMITTED. It cannot see a command pasted into a
// terminal, and it is not admission control. See scripts/approved-suppliers.yml.
type suppliers struct {
	Registries []struct {
		Host   string `yaml:"host"`
		Reason string `yaml:"reason"`
	} `yaml:"registries"`
	Charts []struct {
		URL    string `yaml:"url"`
		Reason string `yaml:"reason"`
	} `yaml:"charts"`
	Endpoints []struct {
		Group  string   `yaml:"group"`
		Reason string   `yaml:"reason"`
		Hosts  []string `yaml:"hosts"`
	} `yaml:"endpoints"`
	Exemptions []struct {
		Path    string `yaml:"path"`
		Rule    string `yaml:"rule"`
		Reason  string `yaml:"reason"`
		Trigger string `yaml:"trigger"`
		Issue   int    `yaml:"issue"`
	} `yaml:"exemptions"`
	Hooks []struct {
		Repo   string `yaml:"repo"`
		Reason string `yaml:"reason"`
	} `yaml:"hooks"`
	Providers []struct {
		Source string `yaml:"source"`
		Reason string `yaml:"reason"`
	} `yaml:"providers"`
}

func readSuppliers(t *testing.T) (suppliers, string) {
	t.Helper()
	root := repoRoot(t)
	path := filepath.Join(root, "scripts", "approved-suppliers.yml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the approved suppliers list: %v", err)
	}
	var s suppliers
	if err := yaml.Unmarshal(body, &s); err != nil {
		t.Fatalf("parsing scripts/approved-suppliers.yml: %v", err)
	}
	if len(s.Registries) == 0 || len(s.Endpoints) == 0 {
		t.Fatal("the suppliers list is empty, so every test below proves nothing")
	}
	return s, root
}

// Every reason has to be filled in. An entry with no reason is somebody
// widening the estate's supply chain without saying why, which is the review
// this file exists to force.
func TestEverySupplierGivesAReason(t *testing.T) {
	s, _ := readSuppliers(t)
	for _, r := range s.Registries {
		if strings.TrimSpace(r.Reason) == "" {
			t.Errorf("registry %q is approved with no reason given", r.Host)
		}
	}
	for _, c := range s.Charts {
		if strings.TrimSpace(c.Reason) == "" {
			t.Errorf("chart source %q is approved with no reason given", c.URL)
		}
	}
	for _, g := range s.Endpoints {
		if strings.TrimSpace(g.Reason) == "" {
			t.Errorf("endpoint group %q is approved with no reason given", g.Group)
		}
	}
	// An exemption is a declared hole in the rules. It has to say why it is
	// there and what would close it, or it is just the guard turned off.
	for _, e := range s.Exemptions {
		if strings.TrimSpace(e.Reason) == "" {
			t.Errorf("exemption for %q gives no reason", e.Path)
		}
		if strings.TrimSpace(e.Trigger) == "" {
			t.Errorf("exemption for %q gives no trigger, so nothing says when it ends", e.Path)
		}
		// An exemption is deferred work. Deferred work that is not on the
		// tracker is a check somebody turned off, so adding one has to cost
		// the same as filing the work it defers.
		if e.Issue <= 0 {
			t.Errorf("exemption for %q names no issue.\n"+
				"File the work it defers and put the number in issue:, or fix the "+
				"thing instead - a red check is not on its own a reason to write an excuse.",
				e.Path)
		}
	}
}

// kustomizeImages reads the image transformer from a kustomization.yaml, if
// one sits beside the manifest. An image pinned there is pinned in what
// actually reaches the cluster, even though the generated manifest it overlays
// still carries a tag - which is the case for Flux's own components, generated
// by `flux bootstrap` and pinned by an overlay rather than by editing a file
// that gets rewritten on every upgrade.
//
// Read rather than shelled out to, so this tier stays hermetic: no kubectl, no
// network, same as every other test in this package.
func kustomizeImages(t *testing.T, dir string) map[string]bool {
	t.Helper()
	pinned := map[string]bool{}

	body, err := os.ReadFile(filepath.Join(dir, "kustomization.yaml"))
	if err != nil {
		return pinned // no overlay here; nothing to honour
	}

	var k struct {
		Images []struct {
			Name   string `yaml:"name"`
			Digest string `yaml:"digest"`
		} `yaml:"images"`
	}
	if err := yaml.Unmarshal(body, &k); err != nil {
		t.Fatalf("parsing %s: %v", filepath.Join(dir, "kustomization.yaml"), err)
	}
	for _, img := range k.Images {
		if strings.HasPrefix(img.Digest, "sha256:") {
			pinned[img.Name] = true
		}
	}
	return pinned
}

var imageRef = regexp.MustCompile(`(?m)^\s*(?:-\s*)?image:\s*["']?([a-zA-Z0-9][a-zA-Z0-9./:@_-]+)["']?\s*$`)

// An image must come from an approved registry and be pinned by digest.
//
// The digest half matters as much as the registry. A tag is a mutable pointer:
// the same name resolves to different software over time and git records
// nothing when it moves, so a tagged image from an approved registry is still
// an unreviewed change waiting to happen.
func TestClusterImagesComeFromApprovedRegistriesAndArePinned(t *testing.T) {
	s, root := readSuppliers(t)

	approved := map[string]bool{}
	for _, r := range s.Registries {
		approved[r.Host] = true
	}

	// Exemptions from the digest rule, and whether each one was actually
	// needed. An excuse that outlives the problem is removed, not kept.
	exempt := map[string]bool{}
	used := map[string]bool{}
	for _, e := range s.Exemptions {
		if e.Rule == "digest-pinned-images" {
			exempt[filepath.FromSlash(e.Path)] = true
		}
	}

	var checked int
	err := filepath.Walk(filepath.Join(root, "clusters"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		overlay := kustomizeImages(t, filepath.Dir(path))
		for _, m := range imageRef.FindAllStringSubmatch(string(body), -1) {
			ref := m[1]
			// A bare name with no registry and no path is a Helm value
			// placeholder or a chart name, not an image reference.
			if !strings.Contains(ref, "/") {
				continue
			}
			checked++

			host, _, _ := strings.Cut(ref, "/")
			if !approved[host] {
				t.Errorf("%s: image %q comes from %q, which is not an approved supplier.\n"+
					"Add it to scripts/approved-suppliers.yml with a reason, or use a registry already approved.",
					rel, ref, host)
			}
			// Pinned either inline, or by the overlay that builds this file.
			name, _, _ := strings.Cut(ref, ":")
			if !strings.Contains(ref, "@sha256:") && !overlay[name] {
				if exempt[rel] {
					used[rel] = true
					continue
				}
				t.Errorf("%s: image %q is not pinned by digest.\n"+
					"A tag is a mutable pointer - the same name is different software later, "+
					"and git records nothing when it moves.\n"+
					"If this genuinely cannot be pinned, declare it under exemptions: in "+
					"scripts/approved-suppliers.yml with a reason and a trigger.", rel, ref)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking clusters/: %v", err)
	}
	if checked == 0 {
		t.Fatal("no image references found under clusters/, so this test proves nothing")
	}
	for path := range exempt {
		if !used[path] {
			t.Errorf("%s is exempted from digest pinning but no longer needs to be.\n"+
				"Remove the exemption from scripts/approved-suppliers.yml - a stale excuse "+
				"reads as a live gap.", path)
		}
	}
}

// Charts decide what images get deployed, so an unapproved chart repository is
// an unapproved registry one level up.
func TestChartSourcesAreApproved(t *testing.T) {
	s, root := readSuppliers(t)

	approved := map[string]bool{}
	for _, c := range s.Charts {
		approved[c.URL] = true
	}

	urlLine := regexp.MustCompile(`(?m)^\s*url:\s*["']?((?:oci|https)://[^\s"']+)["']?\s*$`)

	var checked int
	err := filepath.Walk(filepath.Join(root, "clusters"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, m := range urlLine.FindAllStringSubmatch(string(body), -1) {
			u := m[1]
			// The Flux sync source is this repository itself, not a supplier.
			if strings.Contains(u, "fensberg/homelab") {
				continue
			}
			checked++
			if !approved[u] {
				t.Errorf("%s: chart source %q is not an approved supplier.\n"+
					"Add it to scripts/approved-suppliers.yml with a reason.", rel, u)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking clusters/: %v", err)
	}
	if checked == 0 {
		t.Fatal("no chart sources found under clusters/, so this test proves nothing")
	}
}

// A workflow may not invent its own egress endpoint.
//
// These were restated across fifteen allowlists in nine workflows, so no single
// place answered "what can this repository call out to". Adding one endpoint to
// one workflow was invisible to any review not already reading that file.
func TestWorkflowEgressIsDeclaredInTheSuppliersList(t *testing.T) {
	s, root := readSuppliers(t)

	approved := map[string]bool{}
	for _, g := range s.Endpoints {
		for _, h := range g.Hosts {
			approved[h] = true
		}
	}

	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading workflows: %v", err)
	}

	host := regexp.MustCompile(`^[a-z0-9.-]+:\d+$`)
	var checked int

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}

		var inList bool
		for _, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "allowed-endpoints:") {
				inList = true
				continue
			}
			if !inList {
				continue
			}
			if !host.MatchString(trimmed) {
				inList = false
				continue
			}
			checked++
			if !approved[trimmed] {
				t.Errorf("%s reaches %q, which is not in the approved suppliers list.\n"+
					"Declare it in scripts/approved-suppliers.yml with a reason rather than "+
					"only in the workflow, so one place answers what this repository calls out to.",
					e.Name(), trimmed)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no allowed-endpoints entries found, so this test proves nothing")
	}
}

// Every repository pre-commit clones is a supplier, and must be declared.
//
// This was the delivery nobody was watching. Seven third-party repositories
// run on every commit with the developer's own privileges - not in a
// container, not in CI, on the machine holding the vault session. That is a
// better position than the container image which caused approved-suppliers.yml
// to exist, and none of it was declared anywhere.
//
// It stayed invisible because pre-commit clones into ~/.cache/pre-commit under
// generated names like `repornkulz89`. An eighth repository appearing there
// would look exactly like the seven that belong; the only reason this was ever
// looked at is that somebody saw a strange directory and asked.
func TestEveryCommitHookIsAnApprovedSupplier(t *testing.T) {
	declared := map[string]string{}
	s, _ := readSuppliers(t)
	for _, h := range s.Hooks {
		declared[h.Repo] = h.Reason
	}
	if len(declared) == 0 {
		t.Fatal("no commit hooks are declared as suppliers, so nothing checks what runs " +
			"on every commit")
	}

	used := preCommitRepos(t)
	if len(used) == 0 {
		t.Fatal("no hook repositories were found in .pre-commit-config.yaml. Either the " +
			"file changed shape or this check now guards nothing, and passing on an " +
			"empty set is how a check stops mattering.")
	}

	for _, repo := range used {
		reason, ok := declared[repo]
		if !ok {
			t.Errorf(".pre-commit-config.yaml runs %s and scripts/approved-suppliers.yml "+
				"does not declare it.\n\n"+
				"That repository executes on every commit with the developer's own "+
				"privileges. Add it under `hooks:` with a reason, or remove the hook.",
				repo)
			continue
		}
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%s is declared with no reason. The reason is what a reviewer reads "+
				"when deciding whether this supplier still belongs.", repo)
		}
	}

	// The other direction: a declaration for a hook nobody runs is a supplier
	// approved on paper, and the next person reads the list as current.
	for repo := range declared {
		if !containsString(used, repo) {
			t.Errorf("scripts/approved-suppliers.yml declares %s and "+
				".pre-commit-config.yaml does not use it. A supplier nobody takes "+
				"delivery from makes the list look longer than the exposure is.", repo)
		}
	}
}

// Every hook must be pinned. `rev:` is the whole supply-chain control here:
// a moving reference means the code that runs on the next commit is not the
// code anybody reviewed.
func TestEveryCommitHookIsPinned(t *testing.T) {
	body := readPreCommitConfig(t)
	var current string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- repo:") {
			current = strings.TrimSpace(strings.TrimPrefix(trimmed, "- repo:"))
			if current != "local" && !strings.Contains(body, "rev:") {
				t.Errorf("%s has no rev at all", current)
			}
			continue
		}
		if strings.HasPrefix(trimmed, "rev:") && current != "" && current != "local" {
			rev := strings.TrimSpace(strings.TrimPrefix(trimmed, "rev:"))
			if rev == "" || rev == "main" || rev == "master" || rev == "HEAD" {
				t.Errorf("%s is pinned to %q, which moves. The code that runs on the "+
					"next commit would not be the code anybody reviewed.", current, rev)
			}
			current = ""
		}
	}
}

func preCommitRepos(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(readPreCommitConfig(t), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "- repo:") {
			continue
		}
		repo := strings.TrimSpace(strings.TrimPrefix(trimmed, "- repo:"))
		// `local` is this repository's own hooks - scripts already in the tree,
		// reviewed like everything else, and not a delivery from anybody.
		if repo == "local" || repo == "" {
			continue
		}
		out = append(out, repo)
	}
	return out
}

func readPreCommitConfig(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), ".pre-commit-config.yaml"))
	if err != nil {
		t.Fatalf("reading .pre-commit-config.yaml: %v", err)
	}
	return string(body)
}

func containsString(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// Every OpenTofu provider is a declared supplier, and every declared supplier
// is actually used.
//
// Providers are the most privileged deliveries this estate accepts: binaries
// downloaded at init time and executed locally, each handed a live credential -
// the hypervisor API token, the cluster PKI, the object storage keys. The
// hooks section of approved-suppliers.yml exists because seven repositories run
// on a developer's machine with a developer's privileges; this is the same
// shape at the estate's privilege, and it went undeclared until #230.
//
// Checked in both directions on purpose. One direction stops a provider being
// added to the HCL without anybody deciding to take delivery from that party;
// the other stops the list keeping an entry for a provider nobody uses, which
// is how an approval outlives the thing it approved.
func TestEveryProviderIsADeclaredSupplier(t *testing.T) {
	s, root := readSuppliers(t)

	approved := map[string]bool{}
	for _, p := range s.Providers {
		if strings.TrimSpace(p.Reason) == "" {
			t.Errorf("provider %q is approved with no reason given", p.Source)
		}
		approved[p.Source] = true
	}
	if len(approved) == 0 {
		t.Fatal("approved-suppliers.yml declares no providers, so this test proves nothing")
	}

	body, err := os.ReadFile(filepath.Join(root, "management", "cluster", "versions.tf"))
	if err != nil {
		t.Fatalf("reading versions.tf: %v", err)
	}

	// `name = { source = "owner/name", version = "~> x.y" }`
	declared := map[string]bool{}
	for _, m := range regexp.MustCompile(`source\s*=\s*"([a-zA-Z0-9._-]+/[a-zA-Z0-9._-]+)"`).FindAllStringSubmatch(string(body), -1) {
		declared[m[1]] = true
	}
	if len(declared) == 0 {
		t.Fatal("no providers found in versions.tf, so this test proves nothing")
	}

	for source := range declared {
		if !approved[source] {
			t.Errorf("management/cluster/versions.tf requires the provider %q, which is not an approved supplier.\n\n"+
				"A provider runs with this estate's credentials, so taking delivery from a new party is a "+
				"decision rather than a line of HCL. Declare it in scripts/approved-suppliers.yml with a "+
				"reason, in its own pull request.", source)
		}
	}
	for source := range approved {
		if !declared[source] {
			t.Errorf("scripts/approved-suppliers.yml approves the provider %q and versions.tf does not require it.\n\n"+
				"Remove the entry - an approval that outlives the thing it approved is how a supplier list "+
				"stops describing what the estate actually takes.", source)
		}
	}
}
