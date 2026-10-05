package repo

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A workflow that takes inputs when it is dispatched can publish nothing.
//
// Checkov's CKV_GHA_7 wants a dispatched workflow to take no inputs at all,
// and the reason is a build's: whoever dispatches it would be choosing what
// goes into something that is then published under the repository's name.
// The integration tiers take two - which tier, which site - and build
// nothing, so the rule is excused for the repository as a whole.
//
// That is only sound while it stays true of every workflow, including the
// next one somebody gives an input to. So this is the rule's reason, held:
// a workflow with dispatch inputs declares its permissions, at the top and
// on every job that sets its own, and none of them is a write. Without a
// write it cannot push a commit, a tag, a package, a release or an
// attestation, and there is no output for an input to have shaped.
func TestAWorkflowThatTakesInputsCanPublishNothing(t *testing.T) {
	type workflow struct {
		On          yaml.Node            `yaml:"on"`
		Permissions yaml.Node            `yaml:"permissions"`
		Jobs        map[string]yaml.Node `yaml:"jobs"`
	}
	writes := func(perms *yaml.Node) []string {
		if perms == nil || perms.Kind == 0 {
			return []string{"declares no permissions, so it has whatever the repository's default is"}
		}
		if perms.Kind != yaml.MappingNode {
			if perms.Value == "read-all" || perms.Value == "{}" {
				return nil
			}
			return []string{"has permissions: " + perms.Value}
		}
		var out []string
		for i := 0; i+1 < len(perms.Content); i += 2 {
			if v := perms.Content[i+1].Value; v != "read" && v != "none" {
				out = append(out, "may "+v+" "+perms.Content[i].Value)
			}
		}
		return out
	}

	var failures []string
	read, takeInputs := 0, 0
	for _, rel := range trackedFiles(t) {
		// A workflow, and not an action: an action has no trigger of its own.
		if !runnable(rel) || strings.HasPrefix(filepath.Base(rel), "action.") {
			continue
		}
		read++
		var w workflow
		if err := yaml.Unmarshal([]byte(readRepoFile(t, rel)), &w); err != nil {
			t.Fatalf("%s does not parse: %v", rel, err)
		}
		dispatch := mappingValue(&w.On, "workflow_dispatch")
		if dispatch == nil || mappingValue(dispatch, "inputs") == nil {
			continue
		}
		takeInputs++
		for _, why := range writes(&w.Permissions) {
			failures = append(failures, rel+" takes inputs and "+why)
		}
		for name, job := range w.Jobs {
			if perms := mappingValue(&job, "permissions"); perms != nil {
				for _, why := range writes(perms) {
					failures = append(failures, rel+" takes inputs and its job "+name+" "+why)
				}
			}
		}
	}
	if read == 0 {
		t.Fatal("read no workflows, so nothing was checked")
	}
	t.Logf("%d of %d workflow(s) take inputs", takeInputs, read)
	if len(failures) > 0 {
		sort.Strings(failures)
		t.Errorf("a workflow that takes inputs could publish something:\n\n  %s\n\n"+
			"Whoever dispatches it would choose what goes into what it publishes. Take the input "+
			"away, or the write.", strings.Join(failures, "\n  "))
	}
}

// mappingValue is the value under key in a YAML mapping, or nil.
func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}
