package harness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"homelab/details/repopath"
)

// FluxTree is the directory holding what each cluster reconciles. Named whole
// it is a scope; a path into it names where a manifest is today, and the tree
// is laid out as a shared core and a directory per site, so that moves.
const FluxTree = "clusters"

var (
	fluxOnce    sync.Once
	fluxObjects map[string][]string
	fluxErr     error
)

// FluxManifest is the one tracked manifest in the Flux tree that declares an
// object of this kind and name, wherever in the tree it is, as a path from
// the top of the repository.
//
// By what it declares rather than by path, for the reason every tier's tests
// find OpenTofu that way: a test that opens a path checks that path, and goes
// on checking it after what it guards has moved.
func FluxManifest(kind, name string) (string, error) {
	fluxOnce.Do(func() { fluxObjects, fluxErr = readFluxObjects() })
	if fluxErr != nil {
		return "", fluxErr
	}
	switch found := fluxObjects[kind+"/"+name]; len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", fmt.Errorf("no manifest under %s declares a %s named %q. If it was renamed or removed, the check that reads it has nothing to read", FluxTree, kind, name)
	default:
		return "", fmt.Errorf("%s %q is declared in %d manifests (%s), so there is no one file to read", kind, name, len(found), strings.Join(found, ", "))
	}
}

// Beside is a file in the same directory as a manifest found by what it
// declares: the kustomization that patches it, the values it was rendered
// from.
func Beside(manifest, name string) string {
	return filepath.ToSlash(filepath.Join(filepath.Dir(manifest), name))
}

// readFluxObjects is every object each tracked manifest in the Flux tree
// declares, as "<kind>/<name>" against the manifests declaring it.
func readFluxObjects() (map[string][]string, error) {
	root, err := repopath.Root()
	if err != nil {
		return nil, err
	}
	// proved by TestEveryProgramRunIsNamedByAConstantOrIsTheOperators
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--", FluxTree+"/*.yaml", FluxTree+"/*.yml").Output()
	if err != nil {
		return nil, fmt.Errorf("listing the manifests tracked under %s: %w", FluxTree, err)
	}
	objects := map[string][]string{}
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel == "" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return nil, err
		}
		dec := yaml.NewDecoder(strings.NewReader(string(body)))
		seen := map[string]bool{}
		for {
			var doc struct {
				Kind     string `yaml:"kind"`
				Metadata struct {
					Name string `yaml:"name"`
				} `yaml:"metadata"`
			}
			// The end of the file, or a document that is not YAML: either
			// way there is nothing more this file can be found by.
			if dec.Decode(&doc) != nil {
				break
			}
			id := doc.Kind + "/" + doc.Metadata.Name
			if doc.Kind != "" && !seen[id] {
				seen[id] = true
				objects[id] = append(objects[id], rel)
			}
		}
	}
	if len(objects) == 0 {
		return nil, fmt.Errorf("no manifest under %s declares anything, so nothing there can be found", FluxTree)
	}
	return objects, nil
}
