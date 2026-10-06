package repo

import (
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A value the inventory gives each host is not also given a default by the
// play.
//
// Ansible ranks a play's own vars above the inventory's. So a placeholder in
// a playbook's vars list for something the inventory supplies per host is
// not a fallback: it is what every host is given, and the inventory's value
// is never seen. That is how the storage driver's section refused its first
// real run - the machines' datastore was in the inventory, and "UNSET" was
// what the play read.
//
// What the inventory supplies is read from the program that writes it: every
// host variable it prints into the inventory it builds.
func TestNoPlayGivesADefaultToWhatTheInventorySuppliesPerHost(t *testing.T) {
	hostVar := regexp.MustCompile(`Fprintf\(&inv, "\s{10}([a-z_]+): `)
	supplied := map[string]string{}
	for _, rel := range goFiles(t) {
		if strings.HasSuffix(rel, "_test.go") {
			continue
		}
		for _, m := range hostVar.FindAllStringSubmatch(readRepoFile(t, rel), -1) {
			supplied[m[1]] = rel
		}
	}
	if len(supplied) == 0 {
		t.Fatal("found no host variable written into an inventory, so this is no longer reading the program that writes one")
	}

	plays := 0
	for _, rel := range trackedFiles(t) {
		if !strings.HasSuffix(rel, ".yml") && !strings.HasSuffix(rel, ".yaml") || strings.HasPrefix(rel, ".github/") {
			continue
		}
		var doc []struct {
			Hosts string               `yaml:"hosts"`
			Vars  map[string]yaml.Node `yaml:"vars"`
		}
		if yaml.Unmarshal([]byte(readRepoFile(t, rel)), &doc) != nil {
			continue
		}
		for _, play := range doc {
			if play.Hosts == "" {
				continue
			}
			plays++
			for name, from := range supplied {
				if _, ok := play.Vars[name]; ok {
					t.Errorf(`%s gives %s a value in a play's vars, and the inventory supplies it for each host (%s).

A play's vars outrank the inventory's, so every host is given this one and the
inventory's is never read. Take it out of the vars list; a task that needs it
can require that it is defined.`, rel, name, from)
				}
			}
		}
	}
	if plays == 0 {
		t.Fatal("found no play in the repository, so this looked at nothing")
	}
}
