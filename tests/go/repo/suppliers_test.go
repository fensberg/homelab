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
	} `yaml:"exemptions"`
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
