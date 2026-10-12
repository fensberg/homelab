// Package standin is a vault for the tests of a program that runs the
// 1Password CLI: the test binary itself, on PATH under the name `op`,
// keeping every item as a file.
//
// A program that runs `op` is tested by giving it a program. A function put
// in front of the CLI would be what the test tested, and the code that runs
// the CLI, reads what it prints and reads a write back would never run. The
// writer in the package above has a stand-in of its own, for one item and
// the ways a write to it fails; this one is for a program that reads and
// writes several vaults and is not testing the writer.
//
// A test package uses it in two places: Answer first thing in TestMain, and
// New in each test that wants a vault.
package standin

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// dirVar is how the stand-in, started as `op`, is told where its vaults are.
const dirVar = "STAND_IN_VAULTS"

// TB is the part of testing.TB that New needs, named here so this package
// does not import testing.
type TB interface {
	Helper()
	Fatal(args ...any)
	TempDir() string
	Setenv(key, value string)
}

type held struct {
	Fields []field `json:"fields"`
}

type field struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Answer answers as the CLI when this process was started as one, and says
// whether it was. A TestMain exits with the status when it was.
func Answer() (status int, was bool) {
	dir := os.Getenv(dirVar)
	if dir == "" || filepath.Base(os.Args[0]) != "op" {
		return 0, false
	}
	return answer(dir, os.Args[1:], os.Stdin, os.Stdout, os.Stderr), true
}

// Vaults is the stand-in's state: a directory for each vault.
type Vaults struct {
	t   TB
	dir string
}

// New puts the stand-in on PATH for the rest of the test, holding the named
// vaults and nothing in them.
func New(t TB, vaults ...string) Vaults {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin, dir := t.TempDir(), t.TempDir()
	if err := os.Symlink(self, filepath.Join(bin, "op")); err != nil {
		t.Fatal(err)
	}
	for _, v := range vaults {
		if err := os.MkdirAll(filepath.Join(dir, v), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(dirVar, dir)
	return Vaults{t, dir}
}

// Keep puts one field in an item, making the item when it is not there.
func (v Vaults) Keep(vault, item, name, value string) {
	v.t.Helper()
	it, _ := read(v.dir, vault, item)
	it.Fields = append(it.Fields, field{name, value})
	body, _ := json.Marshal(it)
	if err := os.WriteFile(at(v.dir, vault, item), body, 0o600); err != nil {
		v.t.Fatal(err)
	}
}

// Kept is the value of one field, or "" when the vault holds none.
func (v Vaults) Kept(vault, item, name string) string {
	it, _ := read(v.dir, vault, item)
	value := ""
	for _, f := range it.Fields {
		if f.Label == name {
			value = f.Value
		}
	}
	return value
}

func at(dir, vault, item string) string { return filepath.Join(dir, vault, item+".json") }

func read(dir, vault, item string) (held, error) {
	var it held
	body, err := os.ReadFile(at(dir, vault, item))
	if err != nil {
		return it, err
	}
	return it, json.Unmarshal(body, &it)
}

// answer is the commands a program here gives the CLI, answered the way the
// CLI answers: JSON on stdout, a sentence on stderr and a non-zero exit when
// it refuses.
func answer(dir string, args []string, in io.Reader, out, said io.Writer) int {
	refuse := func(why string) int { fmt.Fprintln(said, "[ERROR] "+why); return 1 }
	flag := func(name string) string {
		for i, a := range args {
			if a == "--"+name && i+1 < len(args) {
				return args[i+1]
			}
			if strings.HasPrefix(a, "--"+name+"=") {
				return strings.TrimPrefix(a, "--"+name+"=")
			}
		}
		return ""
	}
	listed := func(of, key string, name func(string) string) int {
		entries, err := os.ReadDir(of)
		if err != nil {
			return refuse("that isn't a vault")
		}
		list := []map[string]string{}
		for _, e := range entries {
			list = append(list, map[string]string{key: name(e.Name())})
		}
		_ = json.NewEncoder(out).Encode(list)
		return 0
	}
	switch verb := strings.Join(args[:min(2, len(args))], " "); {
	case verb == "whoami":
	case verb == "vault list":
		return listed(dir, "name", func(n string) string { return n })
	case verb == "item list":
		return listed(filepath.Join(dir, flag("vault")), "title", func(n string) string { return strings.TrimSuffix(n, ".json") })
	case verb == "item get" && len(args) > 2:
		body, err := os.ReadFile(at(dir, flag("vault"), args[2]))
		if err != nil {
			return refuse("that isn't an item in this vault")
		}
		_, _ = out.Write(body)
	case verb == "item create":
		body := []byte(`{"fields": []}`)
		if err := os.WriteFile(at(dir, flag("vault"), flag("title")), body, 0o600); err != nil {
			return refuse(err.Error())
		}
		_, _ = out.Write(body)
	case verb == "item edit" && len(args) > 2:
		body, err := io.ReadAll(in)
		if err != nil || len(body) == 0 {
			return refuse("no item was piped in")
		}
		if err := os.WriteFile(at(dir, flag("vault"), args[2]), body, 0o600); err != nil {
			return refuse(err.Error())
		}
	case len(args) == 2 && args[0] == "read":
		parts := strings.Split(strings.TrimPrefix(args[1], "op://"), "/")
		if len(parts) != 3 {
			return refuse("that isn't a reference to a field of an item")
		}
		it, err := read(dir, parts[0], parts[1])
		if err != nil {
			return refuse("that isn't an item in this vault")
		}
		for _, f := range it.Fields {
			if f.Label == parts[2] {
				fmt.Fprintln(out, f.Value)
				return 0
			}
		}
		return refuse("that item has no such field")
	default:
		return refuse("the stand-in does not answer: " + strings.Join(args, " "))
	}
	return 0
}
