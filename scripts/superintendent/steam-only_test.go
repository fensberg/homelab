package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/details/applications"
	"homelab/details/flux"
	"homelab/details/repopath"
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

// A repository shaped like this one: an application with its declaration,
// its pins, its image and its manifests, the estate's own pins and work
// orders, and a site that runs the application.
func releaseRepo(t *testing.T) (git func(...string) string, commit func(msg string, files map[string]string) string) {
	git = gitRepo(t)
	writeFile(t, workorders.Path, `{"orders":[{"name":"tool","context":"tools/tool"}]}`)
	writeFile(t, estatePins, "OTHER_TOOL_VERSION=1.0\n")
	writeFile(t, declaration, thingDeclaration)
	writeFile(t, pinFile, "THING_BUILD_VERSION=20001\n")
	writeFile(t, thingRoot+"/image/Dockerfile", "ARG THING_BUILD_VERSION\nFROM scratch\n")
	writeFile(t, thingRoot+"/production/kustomization.yaml", "# production\nresources: [../base]\n")
	writeFile(t, thingRoot+"/base/deployment.yaml", "kind: Deployment\nspec:\n  replicas: 1\n")
	writeFile(t, siteFile, releasesFile('a'))
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

var siteFile = applications.SiteFilePath("north")

func releasesFile(d byte) string {
	return "apiVersion: source.toolkit.fluxcd.io/v1\nkind: OCIRepository\nmetadata:\n  name: thing\n" +
		"  namespace: flux-system\nspec:\n  ref:\n    tag: \"1.0.16-1\"\n    digest: \"sha256:" + digest(d) + "\"\n"
}

func localJudge() judge {
	return judge{repository: "Example/homelab", orders: workorders.Path, pins: estatePins,
		git: func(args ...string) (string, error) {
			if args[0] == "fetch" {
				return "", nil // a local repository has every commit already
			}
			return gitRunner(args...)
		}}
}

// Under the bypass, a delivery is judged by what its release was built from.
// The supplier's build alone passes, and so does a comment beside it, because
// a comment reaches no cluster. A change to the manifests, the image's
// context, another pin - the application's or the estate's - the
// application's declaration or the estate's work orders waits for a review.
func TestADeliveryRidesTheBypassOnlyWhenTheSuppliersBuildIsAllThatChanged(t *testing.T) {
	git, commit := releaseRepo(t)
	production := git("rev-parse", "HEAD")
	built := map[string]string{}
	at := func(name string, files map[string]string) {
		built[name] = commit(name, files)
		git("reset", "-q", "--hard", production)
	}
	newBuild := "THING_BUILD_VERSION=20002\n"
	at("the supplier's build alone", map[string]string{pinFile: newBuild})
	at("the build and a comment", map[string]string{pinFile: newBuild, thingRoot + "/production/kustomization.yaml": "# production, from the release\nresources: [../base]\n"})
	at("a change to the manifests", map[string]string{pinFile: newBuild, thingRoot + "/base/deployment.yaml": "kind: Deployment\nspec:\n  replicas: 2\n"})
	at("a change to the image's context", map[string]string{pinFile: newBuild, thingRoot + "/image/Dockerfile": "ARG THING_BUILD_VERSION\nFROM scratch\nUSER root\n"})
	at("another pin of the application's", map[string]string{pinFile: newBuild + "EXTRA_VERSION=1\n"})
	at("a pin of the estate's", map[string]string{pinFile: newBuild, estatePins: "OTHER_TOOL_VERSION=9.9\n"})
	at("a change to its declaration", map[string]string{pinFile: newBuild, declaration: strings.Replace(thingDeclaration, `"env": []`, `"env": ["A"]`, 1)})
	at("a change to the estate's orders", map[string]string{pinFile: newBuild, workorders.Path: `{"orders":[{"name":"tool","context":"tools/other"}]}`})
	at("no new build at all", map[string]string{thingRoot + "/production/kustomization.yaml": "# a comment and nothing else\nresources: [../base]\n"})

	for _, tc := range []struct {
		name    string
		wantOK  bool
		wantSay string
	}{
		{"the supplier's build alone", true, ""},
		{"the build and a comment", true, ""},
		{"a change to the manifests", false, "replicas: 2"},
		{"a change to the image's context", false, "Dockerfile"},
		{"another pin of the application's", false, "EXTRA_VERSION"},
		{"a pin of the estate's", false, estatePins},
		{"a change to its declaration", false, declaration},
		{"a change to the estate's orders", false, workorders.Path},
		{"no new build at all", false, "not a new build"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeRegistry(t, map[string]string{"sha256:" + digest('a'): production, "sha256:" + digest('b'): built[tc.name]})
			base := git("rev-parse", "HEAD")
			writeFile(t, siteFile, releasesFile('b'))
			git("commit", "-qam", "deliver "+tc.name)
			head := git("rev-parse", "HEAD")
			t.Cleanup(func() { git("reset", "-q", "--hard", base) })

			problems, err := localJudge().upstreamOnly(siteFile, base, head)
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

// An application is judged by what it declared when the running release was
// built. One that declared no upstream, or no release, or was not there at
// all, has nothing its release may differ by, and its delivery waits.
func TestADeliveryOfAnApplicationThatDeclaresNoUpstreamWaits(t *testing.T) {
	for name, c := range map[string]struct{ declared, want string }{
		"no upstream":      {`{"release": {"version": {"env": [], "pattern": "v", "example": {"line": "v1", "version": "1"}}}}`, "declares no upstream"},
		"no release":       {`{"upstream": {"kind": "steam", "app": "10", "news": "20", "pin": "THING_BUILD_VERSION"}}`, "does not declare how a release"},
		"not there at all": {"", "is not an application"},
	} {
		t.Run(name, func(t *testing.T) {
			git, commit := releaseRepo(t)
			var production string
			if c.declared == "" {
				git("rm", "-rq", thingRoot)
				git("commit", "-qm", "before the application existed")
				production = git("rev-parse", "HEAD")
			} else {
				production = commit("as declared then", map[string]string{declaration: c.declared})
			}
			next := commit("a new build", map[string]string{declaration: thingDeclaration, pinFile: "THING_BUILD_VERSION=20002\n"})
			fakeRegistry(t, map[string]string{"sha256:" + digest('a'): production, "sha256:" + digest('b'): next})
			base := git("rev-parse", "HEAD")
			writeFile(t, siteFile, releasesFile('b'))
			git("add", "-A")
			git("commit", "-qm", "deliver")
			problems, err := localJudge().upstreamOnly(siteFile, base, git("rev-parse", "HEAD"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(problems, "\n"), c.want) {
				t.Errorf("want a refusal saying %q, got %v", c.want, problems)
			}
		})
	}
}

// A release with nothing running to compare it with is a first release, and
// a first release is somebody's decision.
func TestAFirstReleaseIsNotMergedWithoutReview(t *testing.T) {
	git, _ := releaseRepo(t)
	git("rm", "-q", siteFile)
	writeFile(t, siteFile, "kind: OCIRepository\nmetadata:\n  name: other\nspec:\n  ref:\n    tag: \"1.0.0-1\"\n    digest: \"sha256:"+digest('c')+"\"\n")
	git("add", "-A")
	git("commit", "-qm", "a site that does not run it yet")
	base := git("rev-parse", "HEAD")
	writeFile(t, siteFile, releasesFile('b'))
	git("commit", "-qam", "its first release")
	problems, err := localJudge().upstreamOnly(siteFile, base, git("rev-parse", "HEAD"))
	if err != nil || !strings.Contains(strings.Join(problems, "\n"), "nothing to compare its first one with") {
		t.Errorf("a first release: %v, %v", problems, err)
	}
}

// A registry that cannot say what a release was built from is not evidence
// that nothing changed: the verb refuses to merge rather than guessing.
func TestADeliveryIsNotMergedWhenItsSourceCannotBeRead(t *testing.T) {
	git, _ := releaseRepo(t)
	base := git("rev-parse", "HEAD")
	writeFile(t, siteFile, releasesFile('b'))
	git("commit", "-qam", "deliver")
	head := git("rev-parse", "HEAD")
	fakeRegistry(t, map[string]string{}) // knows neither release

	code := enforceStandingOrder([]string{"-author", "procurement[bot]", "-holder", "procurement[bot]",
		"-base", base, "-head", head, "-repository", "Example/homelab", "-bypass"})
	if code == 0 {
		t.Fatal("a delivery whose releases the registry could not describe was merged without review")
	}
}

// Every site's file in this repository reads, with every release in it
// pinned by tag and digest - whichever sites there are and whatever they run.
func TestReleasePinsReadsThisRepositorysSiteFiles(t *testing.T) {
	root, err := repopath.Root()
	if err != nil {
		t.Fatal(err)
	}
	found, err := filepath.Glob(filepath.Join(root, applications.SitesDir, "*", applications.SiteFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		// A site that runs no application pins no release, and has
		// nothing here to read.
		if !strings.Contains(string(body), flux.OCIRepository) {
			continue
		}
		pins, err := releasePins(string(body))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for name, p := range pins {
			if !strings.HasPrefix(p.Digest, "sha256:") || p.Tag == "" {
				t.Errorf("%s: %s read as %+v", f, name, p)
			}
		}
	}
	pins, err := releasePins("# a comment\n" + releasesFile('a') + "---\nkind: Kustomization\nmetadata:\n  name: not-a-source\n")
	if err != nil || len(pins) != 1 || pins["thing"].Tag != "1.0.16-1" || pins["thing"].Digest != "sha256:"+digest('a') {
		t.Errorf("a site's file read as %v, %v", pins, err)
	}
	if _, err := releasePins("kind: Kustomization\nmetadata:\n  name: x\n"); err == nil {
		t.Error("a file that pins no release was read as one that does")
	}
}
