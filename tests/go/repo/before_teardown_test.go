package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"homelab/details/applications"
)

// A workload's declared backup names a container the workload has.
//
// Before a site is destroyed the contractor asks every workload that declared
// one to take a backup (#588), by running a command in a named container of
// the pods a selector picks. The declaration and the manifest are two files,
// and a container renamed in one of them is a backup that is asked for in a
// container that is not there - which fails at the worst moment, as a refusal
// to tear down, or worse, picks no pod at all and reads as nothing to save.
//
// Held of every application's declaration, found by reading them all, so the
// next workload that declares one is held to it too.
func TestEveryDeclaredBackupNamesAContainerAndLabelsTheWorkloadHas(t *testing.T) {
	root := repoRoot(t)
	// Read by the contractor's own reader, so what is held to the manifest is
	// exactly what a teardown would run.
	//
	// No floor on how many there are: an application with no data declares
	// none, and an estate with no application has none. What this refuses is
	// proved against a declaration written here, in
	// TestBackupProblemsNamesWhatADeclarationGetsWrong.
	declarations, err := applications.Backups(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range declarations {
		workload := filepath.Dir(d.Path)
		containers, labels := map[string]bool{}, map[string]bool{}
		for _, manifest := range tracked(t, func(m string) bool {
			return strings.HasPrefix(m, workload+"/") && (strings.HasSuffix(m, ".yaml") || strings.HasSuffix(m, ".yml"))
		}) {
			body, err := os.ReadFile(filepath.Join(root, manifest))
			if err != nil {
				t.Fatal(err)
			}
			podTemplates(t, manifest, body, containers, labels)
		}
		for _, problem := range backupProblems(d, containers, labels) {
			t.Error(problem)
		}
	}
}

// backupProblems is what a declared backup names that the application's own
// pods do not have.
func backupProblems(d applications.BeforeTeardown, containers, labels map[string]bool) []string {
	var problems []string
	if !containers[d.Container] {
		problems = append(problems, fmt.Sprintf("%s runs its backup in the container %q, and no pod the workload declares has one. A backup asked for in a container that is not there refuses every teardown.", d.Path, d.Container))
	}
	for _, pair := range strings.Split(d.Selector, ",") {
		if !labels[strings.TrimSpace(pair)] {
			problems = append(problems, fmt.Sprintf("%s picks its pods by %q, and no pod the workload declares carries that label. A selector that picks nothing reads as a workload that is not running, and its data is destroyed unsaved.", d.Path, pair))
		}
	}
	return problems
}

// The check is held to what it claims, against a workload written here: a
// backup in a sidecar the pod has, picked by labels it carries, is accepted,
// and a container or a label the pod does not have is named.
func TestBackupProblemsNamesWhatADeclarationGetsWrong(t *testing.T) {
	containers, labels := map[string]bool{}, map[string]bool{}
	podTemplates(t, "thing.yaml", []byte("kind: ConfigMap\n---\nkind: Deployment\nspec:\n  template:\n    metadata:\n      labels: {app: thing, tier: data}\n    spec:\n      initContainers:\n        - name: saver\n      containers:\n        - name: server\n"), containers, labels)
	for name, c := range map[string]struct {
		container, selector string
		want                []string
	}{
		"a sidecar the pod has":        {"saver", "app=thing", nil},
		"the pod's main container":     {"server", "app=thing, tier=data", nil},
		"a container it does not have": {"savers", "app=thing", []string{`in the container "savers"`}},
		"a label it does not carry":    {"saver", "app=thing,tier=cache", []string{`picks its pods by "tier=cache"`}},
		"neither":                      {"x", "y=z", []string{`in the container "x"`, `picks its pods by "y=z"`}},
	} {
		got := backupProblems(applications.BeforeTeardown{Path: "thing/application.json", Container: c.container, Selector: c.selector}, containers, labels)
		if len(got) != len(c.want) {
			t.Errorf("%s: want %d problem(s), got %v", name, len(c.want), got)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(strings.Join(got, "\n"), w) {
				t.Errorf("%s: no problem says %s: %v", name, w, got)
			}
		}
	}
}

// podTemplates collects, from one manifest, the name of every container a pod
// template declares - init containers included, since a sidecar is one - and
// every label a pod template carries, as "key=value".
func podTemplates(t *testing.T, rel string, body []byte, containers, labels map[string]bool) {
	t.Helper()
	dec := yaml.NewDecoder(strings.NewReader(string(body)))
	for {
		var doc struct {
			Spec struct {
				Template struct {
					Metadata struct {
						Labels map[string]string `yaml:"labels"`
					} `yaml:"metadata"`
					Spec struct {
						InitContainers []struct {
							Name string `yaml:"name"`
						} `yaml:"initContainers"`
						Containers []struct {
							Name string `yaml:"name"`
						} `yaml:"containers"`
					} `yaml:"spec"`
				} `yaml:"template"`
			} `yaml:"spec"`
		}
		// The end of the file, or a document that is not a workload's.
		if dec.Decode(&doc) != nil {
			return
		}
		for _, c := range doc.Spec.Template.Spec.InitContainers {
			containers[c.Name] = true
		}
		for _, c := range doc.Spec.Template.Spec.Containers {
			containers[c.Name] = true
		}
		for k, v := range doc.Spec.Template.Metadata.Labels {
			labels[k+"="+v] = true
		}
	}
}
