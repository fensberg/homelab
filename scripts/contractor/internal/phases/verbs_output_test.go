package phases

import (
	"encoding/json"
	"io"
	"os"
	"sort"
	"strings"
	"testing"

	"homelab/contractor/config"
	"homelab/details/repopath"
)

// No verb prints a value from the vault.
//
// A converge runs in a public repository's Actions, so every line a phase
// prints is world-readable. The config keeps hostnames, addresses, the site's
// name and the organisation's in a vault for that reason, and a progress line
// that names the bucket it is uploading to has published two of them: a
// bucket is named for the organisation and the site.
//
// The checks that existed each watched one piece of output - the plan's
// summary, the vault report. Nothing watched a verb. This runs each verb
// whole, through the real phases with recording programs on PATH, and
// requires that nothing printed carries any value the config template takes
// from the vault, nor the name those are made into.
//
// Which values those are is read from the template, not listed: every field
// the template fills from the vault, looked up in the fixture the verbs run
// against. A field added tomorrow is covered the day it is added.
func TestNoVerbPrintsAValueFromTheVault(t *testing.T) {
	for name, verb := range map[string]func(*verbFixture){
		"a build": func(f *verbFixture) {
			f.run(t, "overlay", "compute", "cluster", "migrate", "backup", "sterilize")
		},
		"a converge": func(f *verbFixture) {
			f.ctx.Converge = true
			f.run(t, "take-over", "compute", "cluster", "backup", "sterilize")
		},
		"a teardown": func(f *verbFixture) {
			for _, root := range f.ctx.Roots() {
				mustWriteFile(t, root.BackendPgOn, "terraform {}\n")
			}
			tearDown(f.ctx)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newVerbFixture(t)
			secret := vaultValues(t, f.ctx.ConfigRendered)
			if len(secret) < 10 {
				t.Fatalf("found only %d values the template takes from the vault, so this would pass by looking for nothing", len(secret))
			}
			printed := capture(t, func() { verb(f) })
			if strings.TrimSpace(printed) == "" {
				t.Fatal("the verb printed nothing at all, so its output was not captured")
			}
			for _, value := range secret {
				if strings.Contains(printed, value.is) {
					line := ""
					for _, l := range strings.Split(printed, "\n") {
						if strings.Contains(l, value.is) {
							line = strings.TrimSpace(strings.ReplaceAll(l, value.is, "<"+value.field+">"))
							break
						}
					}
					t.Errorf(`%s prints the config's %s, which the template takes from the vault:

    %s

Every line a verb prints is public when it runs in Actions. Say what the thing
is for - "the state bucket", "the first hypervisor" - and not what it is called.`, name, value.field, line)
				}
			}
		})
	}
}

type vaultValue struct{ field, is string }

// vaultValues is every value the fixture holds at a path where the config
// template takes its value from the vault, and the slug a name becomes,
// longest first so a report names the whole value and not a piece of it.
//
// A value that the template also writes as a literal somewhere is left out:
// the vault attests which vendor a credential is for, and the same word is
// in git beside it.
func vaultValues(t *testing.T, rendered string) []vaultValue {
	t.Helper()
	repo, err := repopath.Root()
	if err != nil {
		t.Fatal(err)
	}
	read := func(path string) any {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s is not JSON: %v", path, err)
		}
		return doc
	}
	template, fixture := read(repo+"/config/management.tpl.json"), read(rendered)

	literal := map[string]bool{}
	var literals func(any)
	literals = func(n any) {
		switch v := n.(type) {
		case map[string]any:
			for _, c := range v {
				literals(c)
			}
		case []any:
			for _, c := range v {
				literals(c)
			}
		case string:
			if !strings.Contains(v, "op://") {
				literal[v] = true
			}
		}
	}
	literals(template)

	seen := map[string]bool{}
	var out []vaultValue
	add := func(field, value string) {
		if len(value) < 4 || literal[value] || seen[value] {
			return
		}
		seen[value] = true
		out = append(out, vaultValue{field, value})
	}
	var walk func(tpl, fix any, field string)
	walk = func(tpl, fix any, field string) {
		switch v := tpl.(type) {
		case map[string]any:
			f, _ := fix.(map[string]any)
			for key, child := range v {
				walk(child, f[key], key)
			}
		case string:
			value, ok := fix.(string)
			if !ok || !strings.Contains(v, "op://") {
				return
			}
			add(field, value)
			if field == "name" {
				add("name, as the slug it is made into", config.Slug(value))
			}
		}
	}
	walk(template, fixture, "")
	sort.Slice(out, func(i, j int) bool { return len(out[i].is) > len(out[j].is) })
	return out
}

// capture is everything written to this process's output while fn runs: what
// the phases print, and what the programs they start print through them.
func capture(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = w, w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	defer func() {
		os.Stdout, os.Stderr = stdout, stderr
	}()
	fn()
	_ = w.Close()
	os.Stdout, os.Stderr = stdout, stderr
	return <-done
}
