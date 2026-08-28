package repo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Flux syncs a public repository, so it needs no credential at all.
//
// A GitRepository pointed at an https:// URL on a public repo clones
// anonymously. Giving it a secretRef anyway does not make the sync more
// secure - it makes an access token a live resource attribute in OpenTofu
// state, doing a job that an unauthenticated GET already does. That inverts
// this project's own rule about which secrets are worth generating: the ones
// that end up in state are the expensive ones, and this one buys nothing to
// pay for itself with.
//
// It is also the failure mode that is hardest to notice, because nothing
// breaks. The sync works with the credential and works without it, so the
// only signal that it was ever wrong is somebody reading gitops.tf and
// asking what the secret is for. That is what this test is standing in for.
//
// Reintroducing it would take two steps - a token back in the config, and a
// secretRef back in gotk-sync.yaml - so both are checked. Either one alone is
// inert, which is exactly why neither would be caught by a run failing.

// syncManifest is the subset of gotk-sync.yaml this test has an opinion
// about. Decoding into a struct rather than grepping means a secretRef
// indented into some other block cannot pass by not matching a regexp.
type syncManifest struct {
	Kind string `yaml:"kind"`
	Spec struct {
		URL       string         `yaml:"url"`
		SecretRef map[string]any `yaml:"secretRef"`
	} `yaml:"spec"`
}

func TestFluxSyncsAnonymously(t *testing.T) {
	root := repoRoot(t)
	rel := filepath.Join("clusters", "management", "flux-system", "gotk-sync.yaml")

	// Read by explicit path, not by walking: flux-system is in skipDirs,
	// because it holds Flux's own generated install manifest. gotk-sync.yaml
	// is the one file in there this repository actually writes.
	body, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}

	dec := yaml.NewDecoder(strings.NewReader(string(body)))
	found := false
	for {
		var doc syncManifest
		if err := dec.Decode(&doc); err != nil {
			break
		}
		if doc.Kind != "GitRepository" {
			continue
		}
		found = true

		if doc.Spec.SecretRef != nil {
			t.Errorf(`%s gives the GitRepository a secretRef.

This repository is public. An https:// clone of it needs no credential, so
the secret this points at would be an access token living in OpenTofu state
to authenticate a request that succeeds unauthenticated.

If the repository has been made private, this test is the wrong thing to
change first - re-read the invariant above and decide deliberately, because
the credential comes back with a state-exposure cost attached.`, rel)
		}

		// An ssh:// or git@ URL cannot clone anonymously, so switching the
		// URL is the other way the credential comes back - and it would make
		// the secretRef check above pass while needing a key anyway.
		if !strings.HasPrefix(doc.Spec.URL, "https://") {
			t.Errorf(`%s clones from %q, which is not an anonymous https:// URL.

Anonymous sync is what makes the missing secretRef correct. An ssh remote
needs a deploy key, which is the same credential this test exists to keep
out of state.`, rel, doc.Spec.URL)
		}
	}

	if !found {
		t.Fatalf("%s declares no GitRepository - this test is reading the wrong file", rel)
	}
}

// The other half. A secret nothing references is still a secret in state, so
// the OpenTofu side is checked independently of the manifest.
var gitCredentialTokens = []string{
	"source_control.token",
	"source_control/token",
	"flux_system_git_auth",
}

func TestNoGitCredentialIsCreatedForFlux(t *testing.T) {
	root := repoRoot(t)
	checked := 0

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".tf") && !strings.HasSuffix(path, ".tftest.hcl") {
			return nil
		}
		checked++
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if tok, hit := firstMatch(string(body), gitCredentialTokens); hit {
			rel, _ := filepath.Rel(root, path)
			t.Errorf(`%s references %q.

Anything OpenTofu reads is a value OpenTofu writes to state. A source-control
token there is a live credential recoverable from a leaked state file, and
Flux does not need one to clone a public repository over https.`, rel, tok)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
	if checked < 5 {
		t.Fatalf("only %d OpenTofu files were checked; the walk is wrong", checked)
	}
}

// And the config template, which is where the token would have to be declared
// before OpenTofu could reference it at all.
//
// Decoded rather than grepped. The obvious string to search for is
// "source_control.token", and it would never have matched: the key is nested
// JSON, and the vault path it holds spells the item "source-control" with a
// hyphen. A check that cannot fail is worse than no check, so this one asks
// the structure the question directly - any field under source_control whose
// name suggests a credential, whatever the reference behind it looks like.
func TestNoSourceControlTokenInTheConfigTemplate(t *testing.T) {
	path := filepath.Join(repoRoot(t), "config", "management.tpl.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the config template: %v", err)
	}

	var tpl struct {
		SourceControl map[string]any `json:"source_control"`
	}
	if err := json.Unmarshal(body, &tpl); err != nil {
		t.Fatalf("parsing the config template: %v", err)
	}
	if tpl.SourceControl == nil {
		t.Fatal("the template no longer declares source_control - this test is checking the wrong file")
	}

	for _, field := range []string{"token", "password", "pat", "ssh_key", "deploy_key"} {
		if _, present := tpl.SourceControl[field]; present {
			t.Errorf(`config/management.tpl.json declares source_control.%s.

Declaring it renders it into config/management.rendered.json on every run,
whether or not anything reads it - one more secret on disk for a clone that
needs none.`, field)
		}
	}
}
