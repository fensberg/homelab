package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A Flux source may only name a secret something in this repository creates.
//
// WHAT THIS GUARDS. `secretRef` on a GitRepository is optional, and a public
// repository needs none - the `flux-system` source has never had one and has
// always worked. But a secretRef naming a secret that does not exist does not
// degrade to anonymous access. It fails closed, with AuthenticationFailed, and
// the source never produces an artifact.
//
// That is worse than it sounds, because of what sits downstream. The Kustomization
// reading that source reports "Source artifact not found", which reads as a
// missing tag rather than as a missing secret. And the layer that applies the
// source has wait:true, so it waits for its own GitRepository to go Ready,
// never gets there, and eventually times out - taking the ignition with it,
// since the health gate is upstream of the point of no return.
//
// One line naming a secret that was never created cost a full rebuild:
//
//	flux-production   False   failed to get secret 'flux-system/flux-system':
//	                          secrets "flux-system" not found
//
// The secret was assumed to exist because `flux bootstrap` creates one - but
// Flux is installed here by OpenTofu applying the manifests directly, which
// creates no such secret. An assumption about another tool's side effects,
// written as a reference.
func TestFluxSourcesOnlyNameSecretsThisRepositoryCreates(t *testing.T) {
	created := secretsThisRepositoryCreates(t)

	secretRef := regexp.MustCompile(`(?s)secretRef:\s*\n\s+name:\s*([A-Za-z0-9.\-_${}]+)`)

	root := repoRoot(t)
	dir := filepath.Join(root, "clusters")
	checked, scanned := 0, 0
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}
		// The upstream component manifest is vendored verbatim and is not
		// this repository's to edit.
		if strings.HasSuffix(path, "gotk-components.yaml") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		rel, _ := filepath.Rel(root, path)
		for _, m := range secretRef.FindAllStringSubmatch(string(body), -1) {
			name := m[1]
			checked++
			if !created[name] {
				t.Errorf(`%s names secretRef %q, which nothing in this repository creates.

A Flux source with a secretRef pointing at a missing secret does not fall back
to anonymous access - it fails with AuthenticationFailed and never produces an
artifact. If the repository is public, delete the secretRef. If it is not,
create the secret where it is needed and name it here.

Secrets this repository creates: %v`, rel, name, sortedSecretNames(created))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// The floor is on manifests scanned, not on secretRefs found. Zero
	// secretRefs is the correct state for a public repository and must stay
	// passing - but zero manifests means the walk stopped matching, and this
	// guard would then assert nothing while reporting success.
	if scanned < 10 {
		t.Fatalf("only %d manifest(s) scanned under clusters/, expected at least 10 - the walk has stopped finding them, so this guard proves nothing", scanned)
	}
	t.Logf("scanned %d manifest(s), checked %d secretRef(s)", scanned, checked)
}

// secretsThisRepositoryCreates collects every secret name the estate actually
// makes: kubernetes_secret resources in the ignition tier, and any Secret
// manifest reconciled from git.
func secretsThisRepositoryCreates(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}

	// Every OpenTofu file the repository authored, wherever it is. This read
	// one root's directory, and when the secrets moved to another root it
	// found none and went on passing: a reference to a secret nothing
	// creates is only refused while the list of what is created is whole.
	tfName := regexp.MustCompile(`(?s)resource\s+"kubernetes_secret"\s+"[A-Za-z0-9_]+"\s*\{.*?name\s*=\s*"([^"]+)"`)
	root := repoRoot(t)
	for _, rel := range openTofuSources(t) {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range tfName.FindAllStringSubmatch(string(body), -1) {
			out[m[1]] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("no kubernetes_secret was found in any OpenTofu file, so every secret a Flux source names would look uncreated - or, with none named, this would pass having checked nothing")
	}
	return out
}

func sortedSecretNames(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
