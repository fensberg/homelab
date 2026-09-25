package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"homelab/details/workorders"
)

// A registry that answers for the releases a test publishes: each digest's
// manifest carries the commit it was built from, as the fabricator records it.
func fakeRegistry(t *testing.T, built map[string]string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "anonymous"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer anonymous" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		d := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		sha, ok := built[d]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"annotations": map[string]string{"org.opencontainers.image.revision": "1.0.16-1@sha1:" + sha},
		})
	}))
	t.Cleanup(srv.Close)
	old := registryBase
	registryBase = srv.URL
	t.Cleanup(func() { registryBase = old })
}

// A repository shaped like this one: the pins file, the work orders, the
// image's context, and the overlay and module its release ships.
func releaseRepo(t *testing.T) (git func(...string) string, commit func(msg string, files map[string]string) string) {
	git = gitRepo(t)
	writeFile(t, workorders.Path, `{"orders":[{"name":"valheim","context":"img","release":{"overlay":"env","module":"mod"}}]}`)
	writeFile(t, "scripts/versions.env", "OTHER_TOOL_VERSION=1.0\nVALHEIM_STEAM_BUILD_VERSION=20001\n")
	writeFile(t, "img/Dockerfile", "ARG VALHEIM_STEAM_BUILD_VERSION\nFROM scratch\n")
	writeFile(t, "env/kustomization.yaml", "# production\nresources: [../mod]\n")
	writeFile(t, "mod/deployment.yaml", "kind: Deployment\nspec:\n  replicas: 1\n")
	writeFile(t, "clusters/management/releases.yaml", releasesFile('a'))
	git("add", "-A")
	git("commit", "-qm", "base")
	commit = func(msg string, files map[string]string) string {
		for p, b := range files {
			writeFile(t, p, b)
		}
		git("add", "-A")
		git("commit", "-qm", msg)
		return git("rev-parse", "HEAD")
	}
	return git, commit
}

func releasesFile(d byte) string {
	return "apiVersion: source.toolkit.fluxcd.io/v1\nkind: OCIRepository\nmetadata:\n  name: valheim\n" +
		"  namespace: flux-system\nspec:\n  ref:\n    tag: \"1.0.16-1\"\n    digest: \"sha256:" + digest(d) + "\"\n"
}

// Under the bypass, a delivery is judged by what its release was built from.
// The Steam build alone passes, and so does a comment beside it, because a
// comment reaches no cluster. A change to the manifests, the image's context,
// another pin or the work orders waits for a review.
func TestADeliveryRidesTheBypassOnlyWhenTheSteamBuildIsAllThatChanged(t *testing.T) {
	git, commit := releaseRepo(t)
	production := git("rev-parse", "HEAD")

	steam := commit("steam", map[string]string{"scripts/versions.env": "OTHER_TOOL_VERSION=1.0\nVALHEIM_STEAM_BUILD_VERSION=20002\n"})
	comment := commit("comment", map[string]string{"env/kustomization.yaml": "# production, from the release\nresources: [../mod]\n"})
	manifest := commit("manifest", map[string]string{"mod/deployment.yaml": "kind: Deployment\nspec:\n  replicas: 2\n"})
	context := commit("context", map[string]string{"img/Dockerfile": "ARG VALHEIM_STEAM_BUILD_VERSION\nFROM scratch\nUSER root\n"})

	j := judge{repository: "Example/homelab", releases: "clusters/management/releases.yaml",
		orders: workorders.Path, pin: "scripts/versions.env",
		git: func(args ...string) (string, error) {
			if args[0] == "fetch" {
				return "", nil // a local repository has every commit already
			}
			return gitRunner(args...)
		}}

	for _, tc := range []struct {
		name    string
		builtAt string
		wantOK  bool
		wantSay string
	}{
		{"the Steam build alone", steam, true, ""},
		{"the Steam build and a comment", comment, true, ""},
		{"a change to the manifests", manifest, false, "replicas: 2"},
		{"a change to the image's context", context, false, "img/Dockerfile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeRegistry(t, map[string]string{"sha256:" + digest('a'): production, "sha256:" + digest('b'): tc.builtAt})
			base := git("rev-parse", "HEAD")
			writeFile(t, "clusters/management/releases.yaml", releasesFile('b'))
			git("commit", "-qam", "deliver "+tc.name)
			head := git("rev-parse", "HEAD")
			t.Cleanup(func() { git("reset", "-q", "--hard", base) })

			problems, err := j.steamOnly(base, head)
			if err != nil {
				t.Fatal(err)
			}
			if ok := len(problems) == 0; ok != tc.wantOK {
				t.Fatalf("passed=%v, want %v: %v", ok, tc.wantOK, problems)
			}
			if tc.wantSay != "" && !strings.Contains(strings.Join(problems, "\n"), tc.wantSay) {
				t.Errorf("refused without naming %q: %v", tc.wantSay, problems)
			}
		})
	}
}

// A registry that cannot say what a release was built from is not evidence
// that nothing changed: the verb refuses to merge rather than guessing.
func TestADeliveryIsNotMergedWhenItsSourceCannotBeRead(t *testing.T) {
	git, _ := releaseRepo(t)
	base := git("rev-parse", "HEAD")
	writeFile(t, "clusters/management/releases.yaml", releasesFile('b'))
	git("commit", "-qam", "deliver")
	head := git("rev-parse", "HEAD")
	fakeRegistry(t, map[string]string{}) // knows neither release

	code := enforceStandingOrder([]string{"-author", "procurement[bot]", "-holder", "procurement[bot]",
		"-base", base, "-head", head, "-repository", "Example/homelab", "-bypass"})
	if code == 0 {
		t.Fatal("a delivery whose releases the registry could not describe was merged without review")
	}
}

func TestReleasePinsReadsThisRepositorysReleasesFile(t *testing.T) {
	body, err := os.ReadFile("../../clusters/management/releases.yaml")
	if err != nil {
		t.Fatal(err)
	}
	pins, err := releasePins(string(body))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := pins["valheim"]
	if !ok || !strings.HasPrefix(p.Digest, "sha256:") || p.Tag == "" {
		t.Fatalf("the real releases file read as %v", pins)
	}
}
