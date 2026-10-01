package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func declare(t *testing.T, root, workload, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(ApplicationsDir), workload, TeardownDeclaration)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Every workload that declares is read, with where it was declared; a
// repository where none does has nothing to ask.
func TestDeclaredBackupsReadsEveryWorkloadsDeclaration(t *testing.T) {
	root := t.TempDir()
	if got, err := DeclaredBackups(root); err != nil || len(got) != 0 {
		t.Fatalf("a repository with no declaration: %v, %v", got, err)
	}
	declare(t, root, "beta", `{"what": "its ledger", "namespace": "b", "selector": "app=b", "container": "c", "command": ["save"]}`)
	declare(t, root, "alpha", `{"what": "its data", "namespace": "a", "selector": "app=a,tier=x", "container": "saver", "command": ["/bin/save", "--now"]}`)
	got, err := DeclaredBackups(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Workload != "alpha" || got[1].Workload != "beta" {
		t.Fatalf("got %+v", got)
	}
	a := got[0]
	if a.What != "its data" || a.Namespace != "a" || a.Selector != "app=a,tier=x" || a.Container != "saver" || strings.Join(a.Command, " ") != "/bin/save --now" {
		t.Errorf("alpha was read as %+v", a)
	}
	if a.Path != ApplicationsDir+"/alpha/"+TeardownDeclaration {
		t.Errorf("alpha's declaration is said to be at %q", a.Path)
	}
}

// A declaration that cannot be read, or leaves out what a backup needs, is an
// error. It is never a workload with nothing to save.
func TestADeclarationThatCannotBeReadIsAnError(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":             `what: its data`,
		"no what":              `{"namespace": "n", "selector": "a=b", "container": "c", "command": ["s"]}`,
		"no namespace":         `{"what": "x", "selector": "a=b", "container": "c", "command": ["s"]}`,
		"no selector":          `{"what": "x", "namespace": "n", "container": "c", "command": ["s"]}`,
		"no container":         `{"what": "x", "namespace": "n", "selector": "a=b", "command": ["s"]}`,
		"an empty command":     `{"what": "x", "namespace": "n", "selector": "a=b", "container": "c", "command": []}`,
		"a field nobody reads": `{"what": "x", "namespace": "n", "selector": "a=b", "container": "c", "command": ["s"], "extra": ["unread"]}`,
	} {
		root := t.TempDir()
		declare(t, root, "thing", body)
		if got, err := DeclaredBackups(root); err == nil {
			t.Errorf("%s: read as %+v", name, got)
		}
	}
}
