package applications

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func declare(t *testing.T, root, app, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(Dir), app, Declaration)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

const aBackup = `"before_teardown": {"what": "its data", "selector": "app=a,tier=x", "container": "saver", "command": ["/bin/save", "--now"]}`

// Every application is read, in name order, with what it declared and where;
// a repository with none has nothing to ask.
func TestReadReadsEveryApplicationsDeclaration(t *testing.T) {
	root := t.TempDir()
	if got, err := Read(root); err != nil || len(got) != 0 {
		t.Fatalf("a repository with no applications: %v, %v", got, err)
	}
	declare(t, root, "beta", `{"requires": ["alpha"]}`)
	declare(t, root, "alpha", `{"requires": [], `+aBackup+`}`)
	got, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "alpha" || got[1].Name != "beta" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Path != Dir+"/alpha/"+Declaration {
		t.Errorf("alpha's declaration is said to be at %q", got[0].Path)
	}
	if strings.Join(got[1].Requires, ",") != "alpha" || got[1].BeforeTeardown != nil {
		t.Errorf("beta was read as %+v", got[1])
	}

	// Only those with data a teardown would lose are asked to back up.
	backups, err := Backups(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("backups are %+v", backups)
	}
	b := backups[0]
	if b.Workload != "alpha" || b.What != "its data" || b.Namespace != "alpha" || b.Selector != "app=a,tier=x" || b.Container != "saver" || strings.Join(b.Command, " ") != "/bin/save --now" || b.Path != got[0].Path {
		t.Errorf("alpha's backup was read as %+v", b)
	}
}

// A declaration that cannot be read, or leaves out what it must say, is an
// error. It is never an application with nothing to say.
func TestAnApplicationThatCannotBeReadIsAnError(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":                 `requires: []`,
		"a field nothing reads":    `{"requires": [], "extra": ["unread"]}`,
		"a backup of nothing":      `{"before_teardown": {"selector": "a=b", "container": "c", "command": ["s"]}}`,
		"no selector":              `{"before_teardown": {"what": "x", "container": "c", "command": ["s"]}}`,
		"no container":             `{"before_teardown": {"what": "x", "selector": "a=b", "command": ["s"]}}`,
		"an empty command":         `{"before_teardown": {"what": "y", "selector": "a=b", "container": "c", "command": []}}`,
		"requiring itself":         `{"requires": ["thing"]}`,
		"requiring nothing there":  `{"requires": ["absent"]}`,
		"a route out of the band":  `{"routes": {"door": 64}}`,
		"a route nothing can be":   `{"routes": {"Front_Door": 4}}`,
		"an empty secret":          `{"secrets": {"keys": {}}}`,
		"a key from nowhere":       `{"secrets": {"keys": {"k": {}}}}`,
		"a key from two places":    `{"secrets": {"keys": {"k": {"vault": "a", "value": "b"}}}}`,
		"a bucket fact not known":  `{"secrets": {"keys": {"k": {"storage": "region"}}}}`,
		"a vault field misnamed":   `{"secrets": {"keys": {"k": {"vault": "Server Name"}}}}`,
		"a release of no version":  `{"release": {"version": {"env": ["A"]}}}`,
		"an upstream not known":    `{"upstream": {"kind": "apt", "app": "5", "news": "6", "pin": "A_VERSION"}}`,
		"an upstream of no app":    `{"upstream": {"kind": "steam", "app": "", "news": "2", "pin": "A_VERSION"}}`,
		"an upstream pin misnamed": `{"upstream": {"kind": "steam", "app": "1", "news": "2", "pin": "a version"}}`,
	} {
		root := t.TempDir()
		declare(t, root, "thing", body)
		if got, err := Read(root); err == nil {
			t.Errorf("%s: read as %+v", name, got)
		}
		if _, err := Backups(root); err == nil {
			t.Errorf("%s: its backups were read anyway", name)
		}
	}

	// A directory with no declaration is an application nothing knows about.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(Dir), "silent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(root); err == nil || !strings.Contains(err.Error(), "silent") {
		t.Errorf("an application with no declaration was passed over: %v", err)
	}

	// And a circle of requirements is refused, with the circle named.
	root = t.TempDir()
	declare(t, root, "a", `{"requires": ["b"]}`)
	declare(t, root, "b", `{"requires": ["c"]}`)
	declare(t, root, "c", `{"requires": ["a"]}`)
	if _, err := Read(root); err == nil || !strings.Contains(err.Error(), "a -> b -> c -> a") {
		t.Errorf("a circle of requirements was accepted: %v", err)
	}
}

// What an application declares beyond its requirements is read as it stands:
// its routes, each secret key's one source, how it is released and where its
// upstream publishes; and the vault fields those secrets read are listed once
// each, the generated ones apart.
func TestADeclarationIsReadWhole(t *testing.T) {
	root := t.TempDir()
	declare(t, root, "thing", `{
		"routes": {"front-door": 7},
		"secrets": {
			"thing-keys": {
				"user": {"vault": "user"},
				"again": {"vault": "user"},
				"key": {"generated": "backup_key"},
				"where": {"storage": "bucket"},
				"kind": {"value": ""}
			}
		},
		"release": {"version": {"env": ["A", "B"], "pattern": "v\\([0-9.]*\\)", "example": {"line": "thing v1.2", "version": "1.2"}}},
		"upstream": {"kind": "steam", "app": "10", "news": "20", "pin": "WIDGET_BUILD_VERSION"}
	}`)
	got, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	a := got[0]
	if a.Root != Dir+"/thing" || a.Image() != Dir+"/thing/image" || a.PinsFile() != Dir+"/thing/"+Pins {
		t.Errorf("where it is was read as %q, %q, %q", a.Root, a.Image(), a.PinsFile())
	}
	if a.Routes["front-door"] != 7 || len(a.Routes) != 1 {
		t.Errorf("routes were read as %v", a.Routes)
	}
	keys := a.Secrets["thing-keys"]
	if keys["user"].Vault != "user" || keys["key"].Generated != "backup_key" || keys["where"].Storage != "bucket" || keys["kind"].Value == nil || *keys["kind"].Value != "" {
		t.Errorf("secrets were read as %+v", keys)
	}
	if strings.Join(a.VaultFields(), ",") != "backup_key,user" || strings.Join(a.GeneratedFields(), ",") != "backup_key" {
		t.Errorf("vault fields are %v, generated %v", a.VaultFields(), a.GeneratedFields())
	}
	if a.Release == nil || strings.Join(a.Release.Version.Env, ",") != "A,B" || a.Release.Version.Pattern != `v\([0-9.]*\)` {
		t.Errorf("release was read as %+v", a.Release)
	}
	if u := a.Upstream; u == nil || u.Kind != Steam || u.App != "10" || u.News != "20" || u.Pin != "WIDGET_BUILD_VERSION" {
		t.Errorf("upstream was read as %+v", a.Upstream)
	}
}

// Two applications cannot be given one address: neither the same route name
// nor the same host number.
func TestTwoApplicationsCannotShareARoute(t *testing.T) {
	for name, second := range map[string]string{
		"one name":        `{"routes": {"door": 9}}`,
		"one host number": `{"routes": {"gate": 8}}`,
	} {
		root := t.TempDir()
		declare(t, root, "a", `{"routes": {"door": 8}}`)
		declare(t, root, "b", second)
		if _, err := Read(root); err == nil || !strings.Contains(err.Error(), "both declare") {
			t.Errorf("%s: accepted (%v)", name, err)
		}
	}
	root := t.TempDir()
	declare(t, root, "a", `{"routes": {"door": 8}}`)
	declare(t, root, "b", `{"routes": {"gate": 9}}`)
	if _, err := Read(root); err != nil {
		t.Errorf("two routes that share nothing were refused: %v", err)
	}
}

// An application's name is its namespace, so a directory that cannot name
// one is refused.
func TestAnApplicationsDirectoryMustNameANamespace(t *testing.T) {
	root := t.TempDir()
	declare(t, root, "Not_A_Namespace", `{}`)
	if _, err := Read(root); err == nil {
		t.Error("accepted")
	}
}

// What the application builds and which environments it has settings for are
// read from its directory: an image is a Dockerfile under image/, and an
// environment is any directory but the base that holds a kustomization.
func TestWhatAnApplicationHasIsReadFromItsDirectory(t *testing.T) {
	root := t.TempDir()
	declare(t, root, "thing", `{}`)
	a := mustRead(t, root)[0]
	if a.Builds(root) {
		t.Error("an application with no Dockerfile was said to build an image")
	}
	for _, f := range []string{a.Image()[len(a.Root)+1:] + "/Dockerfile", Base + "/" + Kustomization, "live/" + Kustomization, "notes/readme.txt"} {
		path := filepath.Join(root, filepath.FromSlash(a.Root), filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if !a.Builds(root) {
		t.Error("an application with a Dockerfile was said to build nothing")
	}
	envs, err := a.Environments(root)
	if err != nil || strings.Join(envs, ",") != "live" {
		t.Errorf("environments are %v, %v", envs, err)
	}
}

func mustRead(t *testing.T, root string) []Application {
	t.Helper()
	got, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// A site's applications are read from the paths its blocks reconcile: which
// application, and which environment's settings. A site with no file was
// given none, and one application given twice is refused.
func TestAssignedReadsWhatASiteWasGiven(t *testing.T) {
	root := t.TempDir()
	if got, err := Assigned(root, "site9"); err != nil || len(got) != 0 {
		t.Fatalf("a site with no file: %v, %v", got, err)
	}
	write := func(body string) {
		path := filepath.Join(root, filepath.FromSlash(SiteFilePath("site9")))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if SiteFilePath("site9") != SitesDir+"/site9/"+SiteFile {
		t.Fatalf("a site's file is said to be at %q", SiteFilePath("site9"))
	}
	write("kind: Kustomization\nspec:\n  path: ./" + Dir + "/zeta/staging\n---\nkind: Kustomization\nspec:\n  # path: ./" + Dir + "/commented/out\n  path: ./" + Dir + "/alpha/production\n  other: ./" + Dir + "/not/a-path\n---\nspec:\n  path: ./elsewhere/beta/production\n")
	got, err := Assigned(root, "site9")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (Assignment{"alpha", "production"}) || got[1] != (Assignment{"zeta", "staging"}) {
		t.Errorf("read as %+v", got)
	}
	write("  path: ./" + Dir + "/alpha/production\n---\n  path: ./" + Dir + "/alpha/staging\n")
	if _, err := Assigned(root, "site9"); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("an application given twice was accepted: %v", err)
	}
}

// An application's own tests read its manifests from where the repository's
// guards put them, as JSON, and are told how to be run when they were not.
func TestReadManifestReadsWhatTheGuardsWrote(t *testing.T) {
	var docs []struct {
		Kind string `json:"kind"`
	}
	t.Setenv(ManifestsEnv, "")
	if err := ReadManifest("base/thing.yaml", &docs); err == nil || !strings.Contains(err.Error(), "is not set") {
		t.Errorf("with nothing handed over: %v", err)
	}
	dir := t.TempDir()
	t.Setenv(ManifestsEnv, dir)
	if err := ReadManifest("base/thing.yaml", &docs); err == nil {
		t.Error("a manifest that is not there was read")
	}
	if err := os.MkdirAll(filepath.Join(dir, "base"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "base", "thing.json"), []byte(`[{"kind":"A"},{"kind":"B"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ReadManifest("base/thing.yaml", &docs); err != nil || len(docs) != 2 || docs[1].Kind != "B" {
		t.Errorf("read as %+v, %v", docs, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "base", "thing.json"), []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ReadManifest("base/thing.yaml", &docs); err == nil {
		t.Error("a manifest that is not JSON was read")
	}
}
