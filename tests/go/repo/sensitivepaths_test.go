package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The sensitive-path list is a rule, so it is checked like one.
//
// A tripwire that names a directory which no longer exists does not fail. It
// simply stops matching, and the pull request that renames the directory is
// also the pull request that silently disarms the alarm covering it. That is
// the failure this file exists to catch, and it is the same shape as every
// other rule in this package: a check on the repository's own files, because
// nothing else in the pipeline has an opinion about it.

type sensitivePaths struct {
	Paths []struct {
		Path string `yaml:"path"`
		Why  string `yaml:"why"`
	} `yaml:"paths"`
}

func loadSensitivePaths(t *testing.T) (string, sensitivePaths) {
	t.Helper()
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, ".github", "sensitive-paths.yml"))
	if err != nil {
		t.Fatalf("reading .github/sensitive-paths.yml: %v", err)
	}
	var list sensitivePaths
	if err := yaml.Unmarshal(body, &list); err != nil {
		t.Fatalf("parsing .github/sensitive-paths.yml: %v", err)
	}
	return root, list
}

func TestEverySensitivePathExists(t *testing.T) {
	root, list := loadSensitivePaths(t)

	if len(list.Paths) < 5 {
		t.Fatalf("only %d sensitive paths declared; this is reading the wrong file or the list was gutted", len(list.Paths))
	}

	for _, entry := range list.Paths {
		if strings.TrimSpace(entry.Path) == "" {
			t.Error("an entry has no path")
			continue
		}
		if _, err := os.Stat(filepath.Join(root, entry.Path)); err != nil {
			t.Errorf(`%s is declared sensitive but does not exist.

A tripwire pointed at a missing path does not fail - it stops matching, and
whoever renamed the path also disarmed the alarm on it without noticing.
Update this entry in the same change that moved the file.`, entry.Path)
		}
	}
}

// Every entry has to say why. The reason is the whole payload: a banner
// saying "this is sensitive" is noise, while one saying "probe.go returns a
// Status and never the value, and widening that return type removes the
// guarantee" is a reviewer actually being told what to look for.
func TestEverySensitivePathExplainsItself(t *testing.T) {
	_, list := loadSensitivePaths(t)

	for _, entry := range list.Paths {
		why := strings.TrimSpace(entry.Why)
		if why == "" {
			t.Errorf("%s is declared sensitive with no reason given", entry.Path)
			continue
		}
		// Long enough to be a reason rather than a label. "Important" and
		// "be careful" tell a reviewer nothing they did not already assume.
		if len(why) < 60 {
			t.Errorf(`%s has a reason too short to be useful: %q

The reason is what the reviewer reads at the moment they are deciding whether
to look. Name the property that breaks, not the fact that one exists.`, entry.Path, why)
		}
	}
}

// The code laws must all be covered. Adding a rule to this package without
// covering it here would leave the newest guard as the least protected one.
func TestTheCodeLawsAreCoveredBySensitivePaths(t *testing.T) {
	root, list := loadSensitivePaths(t)

	covered := func(rel string) bool {
		for _, entry := range list.Paths {
			p := strings.TrimSuffix(entry.Path, "/")
			if rel == p || strings.HasPrefix(rel, p+"/") {
				return true
			}
		}
		return false
	}

	// Every guard test in this package, discovered rather than listed, so a
	// new one cannot be added without being covered.
	dir := filepath.Join(root, "tests", "go", "repo")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	found := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		found++
		rel := filepath.Join("tests", "go", "repo", e.Name())
		if !covered(rel) {
			t.Errorf("%s is a code law but no sensitive path covers it", rel)
		}
	}
	if found < 3 {
		t.Fatalf("only %d guard tests found; this test is looking in the wrong place", found)
	}

	// The files the existing rules name explicitly. If a rule guards a file,
	// a change to that file deserves the alarm.
	for _, rel := range []string{
		"scripts/ignite/internal/onepassword/probe.go",
		"config/management.tpl.json",
		"management/cluster/registry.tf",
	} {
		if !covered(rel) {
			t.Errorf("%s is named by a code law but no sensitive path covers it", rel)
		}
	}
}
