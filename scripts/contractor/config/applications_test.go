package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func declare(t *testing.T, root, app, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(ApplicationsDir), app, ApplicationDeclaration)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

const aBackup = `"before_teardown": {"what": "its data", "namespace": "a", "selector": "app=a,tier=x", "container": "saver", "command": ["/bin/save", "--now"]}`

// Every application is read, in name order, with what it declared and where;
// a repository with none has nothing to ask.
func TestApplicationsReadsEveryApplicationsDeclaration(t *testing.T) {
	root := t.TempDir()
	if got, err := Applications(root); err != nil || len(got) != 0 {
		t.Fatalf("a repository with no applications: %v, %v", got, err)
	}
	declare(t, root, "beta", `{"requires": ["alpha"]}`)
	declare(t, root, "alpha", `{"requires": [], `+aBackup+`}`)
	got, err := Applications(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "alpha" || got[1].Name != "beta" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Path != ApplicationsDir+"/alpha/"+ApplicationDeclaration {
		t.Errorf("alpha's declaration is said to be at %q", got[0].Path)
	}
	if strings.Join(got[1].Requires, ",") != "alpha" || got[1].BeforeTeardown != nil {
		t.Errorf("beta was read as %+v", got[1])
	}

	// Only those with data a teardown would lose are asked to back up.
	backups, err := DeclaredBackups(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("backups are %+v", backups)
	}
	b := backups[0]
	if b.Workload != "alpha" || b.What != "its data" || b.Namespace != "a" || b.Selector != "app=a,tier=x" || b.Container != "saver" || strings.Join(b.Command, " ") != "/bin/save --now" || b.Path != got[0].Path {
		t.Errorf("alpha's backup was read as %+v", b)
	}
}

// A declaration that cannot be read, or leaves out what it must say, is an
// error. It is never an application with nothing to say.
func TestAnApplicationThatCannotBeReadIsAnError(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":                `requires: []`,
		"a field nobody reads":    `{"requires": [], "extra": ["unread"]}`,
		"a backup of nothing":     `{"before_teardown": {"namespace": "n", "selector": "a=b", "container": "c", "command": ["s"]}}`,
		"no namespace":            `{"before_teardown": {"what": "x", "selector": "a=b", "container": "c", "command": ["s"]}}`,
		"no selector":             `{"before_teardown": {"what": "x", "namespace": "n", "container": "c", "command": ["s"]}}`,
		"no container":            `{"before_teardown": {"what": "x", "namespace": "n", "selector": "a=b", "command": ["s"]}}`,
		"an empty command":        `{"before_teardown": {"what": "x", "namespace": "n", "selector": "a=b", "container": "c", "command": []}}`,
		"requiring itself":        `{"requires": ["thing"]}`,
		"requiring nothing there": `{"requires": ["absent"]}`,
	} {
		root := t.TempDir()
		declare(t, root, "thing", body)
		if got, err := Applications(root); err == nil {
			t.Errorf("%s: read as %+v", name, got)
		}
		if _, err := DeclaredBackups(root); err == nil {
			t.Errorf("%s: its backups were read anyway", name)
		}
	}

	// A directory with no declaration is an application nothing knows about.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(ApplicationsDir), "silent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Applications(root); err == nil || !strings.Contains(err.Error(), "silent") {
		t.Errorf("an application with no declaration was passed over: %v", err)
	}

	// And a circle of requirements is refused, with the circle named.
	root = t.TempDir()
	declare(t, root, "a", `{"requires": ["b"]}`)
	declare(t, root, "b", `{"requires": ["c"]}`)
	declare(t, root, "c", `{"requires": ["a"]}`)
	if _, err := Applications(root); err == nil || !strings.Contains(err.Error(), "a -> b -> c -> a") {
		t.Errorf("a circle of requirements was accepted: %v", err)
	}
}
