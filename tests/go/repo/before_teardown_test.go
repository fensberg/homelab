package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"homelab/contractor/config"
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
// Held of every declaration under modules/applications/, found by walking, so
// the next workload that declares one is held to it too.
func TestEveryDeclaredBackupNamesAContainerAndLabelsTheWorkloadHas(t *testing.T) {
	root := repoRoot(t)
	// Read by the contractor's own reader, so what is held to the manifest is
	// exactly what a teardown would run.
	declarations, err := config.DeclaredBackups(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(declarations) == 0 {
		t.Fatal("no workload declares a backup, so either none has data or this has stopped finding the declarations")
	}
	for _, d := range declarations {
		rel := d.Path
		workload := filepath.Dir(rel)
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
		if !containers[d.Container] {
			t.Errorf("%s runs its backup in the container %q, and no pod the workload declares has one. A backup asked for in a container that is not there refuses every teardown.", rel, d.Container)
		}
		for _, pair := range strings.Split(d.Selector, ",") {
			if !labels[strings.TrimSpace(pair)] {
				t.Errorf("%s picks its pods by %q, and no pod the workload declares carries that label. A selector that picks nothing reads as a workload that is not running, and its data is destroyed unsaved.", rel, pair)
			}
		}
		if !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(d.Namespace) {
			t.Errorf("%s names the namespace %q, which is not one", rel, d.Namespace)
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
