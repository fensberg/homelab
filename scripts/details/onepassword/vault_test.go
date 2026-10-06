package onepassword

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A vault for the writer's tests: this test binary, on PATH under the name
// `op`.
//
// The writer runs the 1Password CLI as a program, so its tests give it a
// program: a stand-in in front of it would test the stand-in, and the first
// tests of WriteItem did exactly that. They replaced the one function that
// runs the CLI, so the code that captures what the CLI says on a failure -
// the fix they were written for - never ran, and neither did the write or the
// read-back that follow a create.
//
// The stand-in keeps one item, in a file, and answers the five commands the
// writer uses the way the CLI does: JSON on stdout, a sentence on stderr and
// a non-zero exit when it refuses.
func TestMain(m *testing.M) {
	if dir := os.Getenv(vaultDirVar); dir != "" && filepath.Base(os.Args[0]) == "op" {
		os.Exit(standInVault(dir, os.Args[1:]))
	}
	os.Exit(m.Run())
}

const vaultDirVar = "STAND_IN_VAULT"

// vault is the stand-in's state: files in a directory.
type vault struct{ dir string }

func newVault(t *testing.T) vault {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin, dir := t.TempDir(), t.TempDir()
	if err := os.Symlink(self, filepath.Join(bin, "op")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(vaultDirVar, dir)
	return vault{dir}
}

func (v vault) set(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(v.dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ran is the commands the vault was given, one to a line.
func (v vault) ran() string {
	b, _ := os.ReadFile(filepath.Join(v.dir, "ran"))
	return string(b)
}

func standInVault(dir string, args []string) int {
	in := func(name string) string { return filepath.Join(dir, name) }
	exists := func(name string) bool { _, err := os.Stat(in(name)); return err == nil }
	refuse := func(code int, why string) int {
		fmt.Fprintln(os.Stderr, "[ERROR] "+why)
		return code
	}
	log, _ := os.OpenFile(in("ran"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	fmt.Fprintln(log, strings.Join(args, " "))
	log.Close()

	switch {
	case len(args) >= 2 && args[0] == "item" && args[1] == "get":
		if exists("get_times_out") {
			return refuse(1, "the request timed out")
		}
		body, err := os.ReadFile(in("item.json"))
		if err != nil {
			return refuse(1, "that isn't an item in this vault")
		}
		os.Stdout.Write(body)
	case len(args) >= 2 && args[0] == "item" && args[1] == "list":
		body, err := os.ReadFile(in("list.json"))
		if err != nil {
			body = []byte("[]")
		}
		os.Stdout.Write(body)
	case len(args) >= 2 && args[0] == "item" && args[1] == "create":
		if exists("create_refused") {
			return refuse(3, "the vault said why")
		}
		body := []byte(`{"fields": []}`)
		if err := os.WriteFile(in("item.json"), body, 0o600); err != nil {
			return refuse(1, err.Error())
		}
		os.Stdout.Write(body)
	case len(args) >= 2 && args[0] == "item" && args[1] == "edit":
		body, err := io.ReadAll(os.Stdin)
		if err != nil || len(body) == 0 {
			return refuse(1, "no item was piped in")
		}
		if exists("edit_lands_elsewhere") {
			return 0
		}
		if err := os.WriteFile(in("item.json"), body, 0o600); err != nil {
			return refuse(1, err.Error())
		}
	case len(args) == 2 && args[0] == "read":
		parts := strings.Split(strings.TrimPrefix(args[1], "op://"), "/")
		section, field := "", parts[len(parts)-1]
		if len(parts) == 4 {
			section = parts[2]
		}
		body, err := os.ReadFile(in("item.json"))
		if err != nil {
			return refuse(1, "that isn't an item in this vault")
		}
		var it item
		if err := json.Unmarshal(body, &it); err != nil {
			return refuse(1, err.Error())
		}
		sectionID := ""
		for _, s := range it.Sections {
			if s.Label == section || s.ID == section {
				sectionID = s.ID
			}
		}
		for _, f := range it.Fields {
			if f.Label == field && f.Section.ID == sectionID {
				fmt.Println(f.Value)
				return 0
			}
		}
		return refuse(1, "that item has no such field")
	default:
		return refuse(2, "the stand-in vault was asked for something the writer does not use: "+strings.Join(args, " "))
	}
	return 0
}

// An item the vault does not hold is made, its fields written, and each read
// back before the write is believed.
func TestAnItemTheVaultDoesNotHoldIsMadeWrittenAndReadBack(t *testing.T) {
	v := newVault(t)
	changed, err := WriteItem("a-vault", "an_item", map[string]string{"first": "one", "second": "two"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(changed, ",") != "first,second" {
		t.Errorf("changed %v", changed)
	}
	for _, want := range []string{"item create", "item edit an_item", "read op://a-vault/an_item/first", "read op://a-vault/an_item/second"} {
		if !strings.Contains(v.ran(), want) {
			t.Errorf("the vault was never asked to %q:\n%s", want, v.ran())
		}
	}

	// And again with the same values: nothing differs, so nothing is written.
	before := v.ran()
	changed, err = WriteItem("a-vault", "an_item", map[string]string{"first": "one", "second": "two"})
	if err != nil || len(changed) != 0 {
		t.Fatalf("a second write of the same values changed %v, %v", changed, err)
	}
	if strings.Contains(strings.TrimPrefix(v.ran(), before), "item edit") {
		t.Error("an item whose fields were already right was written again, which reads as a rotation to everything that watches it")
	}
}

// An item is made only when the vault has none of that title. Two runs that
// both found it absent, or a read that merely failed, would otherwise leave
// two, and a reference to a title held twice resolves to neither.
func TestAnItemThatIsThereAndUnreadableIsNotMadeASecondTime(t *testing.T) {
	v := newVault(t)
	v.set(t, "get_times_out", "")
	v.set(t, "list.json", `[{"title": "something_else"}, {"title": "an_item"}]`)

	_, err := WriteItem("a-vault", "an_item", map[string]string{"first": "one"})
	if err == nil || !strings.Contains(err.Error(), "the request timed out") {
		t.Fatalf("got %v, want a refusal carrying what the vault said", err)
	}
	if strings.Contains(v.ran(), "item create") {
		t.Errorf("a second item of the same title was created:\n%s", v.ran())
	}
}

// What the vault says when it refuses reaches whoever is reading the failure.
// It used to be the exit status and nothing else.
func TestARefusalToMakeAnItemSaysWhatTheVaultSaid(t *testing.T) {
	v := newVault(t)
	v.set(t, "create_refused", "")

	_, err := WriteItem("a-vault", "an_item", map[string]string{"first": "one"})
	if err == nil {
		t.Fatal("a refused create was reported as a write")
	}
	for _, want := range []string{"exit status 3", "the vault said why"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not carry %q: %v", want, err)
		}
	}
}

// A write the vault accepted and did not keep is caught by reading it back.
func TestAWriteThatDidNotLandIsNotBelieved(t *testing.T) {
	v := newVault(t)
	v.set(t, "item.json", `{"fields": [{"id": "first", "label": "first", "value": "old"}]}`)
	v.set(t, "edit_lands_elsewhere", "")

	_, err := WriteItem("a-vault", "an_item", map[string]string{"first": "new"})
	if err == nil || !strings.Contains(err.Error(), "did not round-trip") {
		t.Fatalf("got %v, want the read-back to refuse it", err)
	}
}

// A value this program generates is made once: stored when the field is
// absent, and handed back untouched when it is there.
func TestAGeneratedValueIsMadeOnceAndKept(t *testing.T) {
	newVault(t)
	ref := Ref{Vault: "a-vault", Item: "an_item", Field: "password"}
	made := 0
	generate := func() (string, error) { made++; return "generated-once", nil }

	value, status, err := EnsureField(ref, generate)
	if err != nil || value != "generated-once" || status != "generated" {
		t.Fatalf("first: %q, %q, %v", value, status, err)
	}
	value, status, err = EnsureField(ref, generate)
	if err != nil || value != "generated-once" || status != "existing" {
		t.Fatalf("second: %q, %q, %v", value, status, err)
	}
	if made != 1 {
		t.Errorf("generated %d times. A credential state already depends on was replaced under it", made)
	}
}

const anItemWithASection = `{
  "sections": [{"id": "s1", "label": "database"}],
  "fields": [{"id": "f1", "label": "password", "value": "old", "section": {"id": "s1"}},
             {"id": "f2", "label": "password", "value": "another-items-own"}]}`

// A field in a section is written there and nowhere else, and read back from
// there.
func TestAFieldInASectionIsWrittenThereAndReadBack(t *testing.T) {
	v := newVault(t)
	v.set(t, "item.json", anItemWithASection)
	ref := Ref{Vault: "a-vault", Item: "an_item", Section: "database", Field: "password"}

	if err := WriteField(ref, "new"); err != nil {
		t.Fatal(err)
	}
	if got, err := Read(ref.String()); err != nil || got != "new" {
		t.Errorf("the section's field reads %q, %v", got, err)
	}
	if got, err := Read("op://a-vault/an_item/password"); err != nil || got != "another-items-own" {
		t.Errorf("the field of the same name outside the section reads %q, %v: the write landed in the wrong place", got, err)
	}
}

func TestAFieldCannotBeWrittenToAnItemThatIsNotThere(t *testing.T) {
	newVault(t)
	err := WriteField(Ref{Vault: "a-vault", Item: "an_item", Section: "database", Field: "password"}, "new")
	if err == nil || !strings.Contains(err.Error(), "reading item") {
		t.Fatalf("got %v", err)
	}
}
