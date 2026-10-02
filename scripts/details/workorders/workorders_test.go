package workorders

import (
	"os"
	"path/filepath"
	"testing"

	"homelab/details/applications"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The orders are the estate's own, then one for every application that has
// an image and for no other: named for its directory, built from its image
// directory, pinned by its own pins file, and released - when its
// declaration says how - from its directory, whole.
func TestReadComposesTheEstatesOrdersAndTheApplications(t *testing.T) {
	root := t.TempDir()
	write(t, root, Path, `{"orders":[{"name":"gadget","context":"tools/gadget"}]}`)
	write(t, root, applications.Dir+"/released/"+applications.Declaration, `{"release": {"version": {"env": ["A"], "pattern": "v\\([0-9.]*\\)", "example": {"line": "thing v1.2", "version": "1.2"}}}}`)
	write(t, root, applications.Dir+"/released/image/Dockerfile", "FROM scratch\n")
	write(t, root, applications.Dir+"/compiled/"+applications.Declaration, `{}`)
	write(t, root, applications.Dir+"/compiled/image/Dockerfile", "FROM scratch\n")
	write(t, root, applications.Dir+"/imageless/"+applications.Declaration, `{}`)

	orders, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 3 || orders[0].Name != "gadget" || orders[1].Name != "compiled" || orders[2].Name != "released" {
		t.Fatalf("orders are %+v", orders)
	}
	if o := orders[0]; o.Context != "tools/gadget" || o.Pins != "" || o.Release != nil {
		t.Errorf("the estate's own order was read as %+v", o)
	}
	if o := orders[1]; o.Context != applications.Dir+"/compiled/image" || o.Pins != applications.Dir+"/compiled/"+applications.Pins || o.Release != nil {
		t.Errorf("an application with an image and no release was read as %+v", o)
	}
	o := orders[2]
	if o.Release == nil || o.Release.Module != applications.Dir+"/released" || len(o.Release.Version.Env) != 1 || o.Release.Version.Env[0] != "A" || o.Release.Version.Pattern != `v\([0-9.]*\)` || o.Release.Version.Example.Version != "1.2" {
		t.Errorf("an application that is released was read as %+v (%+v)", o, o.Release)
	}
}

// What cannot be read builds nothing, and says so: no orders file, an empty
// one, and an application whose declaration does not parse.
func TestReadRefusesWhatItCannotRead(t *testing.T) {
	root := t.TempDir()
	if _, err := Read(root); err == nil {
		t.Error("a repository with no orders file was read")
	}
	if _, err := Parse([]byte(`{"orders":[]}`)); err == nil {
		t.Error("an empty list of orders was accepted")
	}
	if _, err := Parse([]byte(`orders:`)); err == nil {
		t.Error("orders that are not JSON were accepted")
	}
	// An order is for one thing: an image to build, or a package to publish.
	for name, order := range map[string]string{
		"neither": `{"name":"x"}`,
		"both":    `{"name":"x","context":"a","package":"b"}`,
		"no name": `{"context":"a"}`,
	} {
		if _, err := Parse([]byte(`{"orders":[` + order + `]}`)); err == nil {
			t.Errorf("an order with %s was accepted", name)
		}
	}
	if got, err := Parse([]byte(`{"orders":[{"name":"x","package":"b"}]}`)); err != nil || got[0].Package != "b" || got[0].Context != "" {
		t.Errorf("an order that publishes a package was read as %+v, %v", got, err)
	}
	write(t, root, Path, `{"orders":[{"name":"gadget","context":"tools/gadget"}]}`)
	write(t, root, applications.Dir+"/broken/"+applications.Declaration, `{"nobody_reads": true}`)
	if _, err := Read(root); err == nil {
		t.Error("orders were read past a declaration that does not parse")
	}
}

// The repository's own orders read, each with a name and a context.
func TestTheRepositorysWorkOrdersRead(t *testing.T) {
	orders, err := Read("../../..")
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) == 0 {
		t.Fatal("the repository reads as having no orders")
	}
}
