package tofustate

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTF(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Every moved block is read as a pair, in a stable order; a `moved` word
// elsewhere and quoted strings are not mistaken for one.
func TestMovesAreReadAsPairs(t *testing.T) {
	dir := t.TempDir()
	writeTF(t, dir, "b.tf", "moved {\n  from = a.old\n  to   = a.new\n}\n")
	writeTF(t, dir, "a.tf", "# the database moved last week\nmoved {\n  from = module.x.b[\"k\"]\n  to   = b.y[\"k\"]\n}\nresource \"x\" \"y\" { note = \"moved\" }\n")
	writeTF(t, dir, "notes.md", "moved {\n from = c.d\n to = c.e\n}\n")
	got, err := Moves(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Move{{From: `module.x.b["k"]`, To: `b.y["k"]`}, {From: "a.old", To: "a.new"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAMoveMissingAnEndIsRefused(t *testing.T) {
	dir := t.TempDir()
	writeTF(t, dir, "a.tf", "moved {\n  from = a.old\n}\n")
	if _, err := Moves(dir); err == nil {
		t.Error("a move with no to was accepted")
	}
	if _, err := Moves(filepath.Join(dir, "missing")); err == nil {
		t.Error("a directory that does not exist was read")
	}
}
