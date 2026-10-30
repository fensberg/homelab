package parsed

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"homelab/details/applications"
	"homelab/details/drafter"
	"homelab/details/flux"
	"homelab/tests/harness"
)

// RenderedLayers is what Flux applies, as the configuration rules are handed
// it: each layer - a Flux Kustomization - with the documents kustomize builds
// from its path, before anything is substituted, with the stand-ins
// validation derives for what the platform hands a site's applications. Each
// is written
// as a file of the test's own, which the rules find by what it holds.
//
// Rendered by the drafter, the estate's own kustomize, so what a rule judges
// is what kustomize builds and not what this reads a directory to list. A
// layer pointed at a directory by a variable of its own is rendered once for
// each directory a site can be pointed at: one with a listing at its top.
//
// And every directory of an application with a listing, whether a site runs
// it or not, with no layer: validation substitutes into each of those too,
// so each is read for the variables it names.
//
// The stand-ins are handed in by the caller, as the script validation runs
// derives them (StandIns reads its listing): part of each layer as
// validation renders it, which substitutes them into every layer alike.
func (r *Tree) RenderedLayers(t testing.TB, derived map[string]string) []string {
	t.Helper()
	layers := r.fluxLayers(t)
	if len(layers) == 0 {
		t.Fatalf("no Flux Kustomization is declared under %s, so there is nothing Flux applies to render", harness.FluxTree)
	}
	var dirs []string
	seen := map[string]bool{}
	for i := range layers {
		for _, dir := range layers[i].dirs {
			if !seen[dir] {
				seen[dir] = true
				dirs = append(dirs, dir)
			}
		}
	}
	var unlayered []string
	for _, rel := range r.Files(t) {
		if strings.HasPrefix(rel, applications.Dir+"/") && path.Base(rel) == applications.Kustomization {
			if dir := path.Dir(rel); !seen[dir] {
				seen[dir] = true
				unlayered = append(unlayered, dir)
			}
		}
	}
	sort.Strings(unlayered)
	dirs = append(dirs, unlayered...)
	sort.Strings(dirs)
	drawn, unbuilt := r.render(dirs)
	out := t.TempDir()
	var files []string
	write := func(name string, v any) {
		body, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(out, name)
		if err := os.WriteFile(file, body, 0o600); err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	for i, l := range layers {
		for j, dir := range l.dirs {
			write(fmt.Sprintf("layer-%d-%d.json", i, j), renderedLayer{
				Layer:     layerHeader{File: l.File, Name: l.Name, Namespace: l.Namespace, Path: dir, Substitute: l.Substitute, SubstituteFrom: l.SubstituteFrom, DerivedStandIns: derived, Unbuilt: unbuilt[dir]},
				Documents: documentsOf(drawn, dir),
			})
		}
	}
	for i, dir := range unlayered {
		write(fmt.Sprintf("unlayered-%d.json", i), renderedLayer{Layer: layerHeader{Path: dir, DerivedStandIns: derived, Unbuilt: unbuilt[dir]}, Documents: documentsOf(drawn, dir)})
	}
	return files
}

// render has the drafter build every directory at once. When that fails,
// each is built alone, so that one kustomize cannot build is known by its
// error and the rest are still rendered: a rule over what Flux applies
// refuses the one, and every other rule is still judged.
func (r *Tree) render(dirs []string) (map[string]json.RawMessage, map[string]string) {
	unbuilt := map[string]string{}
	drawn, err := drafter.Draw(r.root, dirs)
	if err == nil {
		return drawn, unbuilt
	}
	batch := err
	drawn = map[string]json.RawMessage{}
	for _, dir := range dirs {
		documents, why := r.renderOne(dir)
		if why != "" {
			unbuilt[dir] = why
		} else {
			drawn[drafter.Built+dir] = documents
		}
	}
	// Every directory built alone and not together: what failed is not
	// known, so none of it is taken as read.
	if len(unbuilt) == 0 {
		for _, dir := range dirs {
			unbuilt[dir] = batch.Error()
		}
		drawn = map[string]json.RawMessage{}
	}
	return drawn, unbuilt
}

// renderOne is what one directory builds to, or why it does not build.
func (r *Tree) renderOne(dir string) (json.RawMessage, string) {
	one, err := drafter.Draw(r.root, []string{dir})
	if err != nil {
		return nil, err.Error()
	}
	return one[drafter.Built+dir], ""
}

// documentsOf is what a directory was built to, or none where it was not.
func documentsOf(drawn map[string]json.RawMessage, dir string) json.RawMessage {
	if documents, ok := drawn[drafter.Built+dir]; ok {
		return documents
	}
	return json.RawMessage("[]")
}

// renderedLayer is one layer as a rule reads it.
type renderedLayer struct {
	Layer     layerHeader     `json:"layer"`
	Documents json.RawMessage `json:"documents"`
}

// layerHeader is what a layer says of itself: where it is declared, the
// directory it applies, and what it substitutes from.
type layerHeader struct {
	File           string            `json:"file"`
	Name           string            `json:"name"`
	Namespace      string            `json:"in_namespace"`
	Path           string            `json:"path"`
	Substitute     map[string]string `json:"substitute"`
	SubstituteFrom []SubstituteFrom  `json:"substitute_from"`
	// DerivedStandIns is what validation substitutes for what the platform
	// hands a site's applications, by name.
	DerivedStandIns map[string]string `json:"derived_stand_ins"`
	// Unbuilt is why kustomize could not build the directory, or "".
	Unbuilt string `json:"unbuilt,omitempty"`
}

// SubstituteFrom is one place a layer's variables are filled from.
type SubstituteFrom struct {
	Kind string `json:"kind" yaml:"kind"`
	Name string `json:"name" yaml:"name"`
}

// fluxLayer is a Flux Kustomization, and the directories it applies.
type fluxLayer struct {
	File, Name     string
	Namespace      string
	Substitute     map[string]string
	SubstituteFrom []SubstituteFrom
	dirs           []string
}

// fluxLayers is every Flux Kustomization declared in the tree's manifests,
// found by what it is.
func (r *Tree) fluxLayers(t testing.TB) []fluxLayer {
	t.Helper()
	var sites []string
	for _, e := range r.Entries(t, harness.FluxTree) {
		if dir := harness.FluxTree + "/" + e; r.IsDir(dir) && r.Exists(dir+"/"+applications.Kustomization) {
			sites = append(sites, dir)
		}
	}
	var out []fluxLayer
	for _, rel := range r.Files(t) {
		if !strings.HasPrefix(rel, harness.FluxTree+"/") || !(strings.HasSuffix(rel, ".yaml") || strings.HasSuffix(rel, ".yml")) {
			continue
		}
		for _, d := range r.YAMLDocuments(t, rel) {
			var doc struct {
				APIVersion string `yaml:"apiVersion"`
				Kind       string `yaml:"kind"`
				Metadata   struct {
					Name      string `yaml:"name"`
					Namespace string `yaml:"namespace"`
				} `yaml:"metadata"`
				Spec struct {
					Path      string `yaml:"path"`
					PostBuild struct {
						Substitute     map[string]string `yaml:"substitute"`
						SubstituteFrom []SubstituteFrom  `yaml:"substituteFrom"`
					} `yaml:"postBuild"`
				} `yaml:"spec"`
			}
			if m, ok := d.(map[string]any); !ok || m["kind"] != flux.Kustomization {
				continue
			}
			encoded, err := yaml.Marshal(d)
			if err == nil {
				err = yaml.Unmarshal(encoded, &doc)
			}
			if err != nil {
				t.Fatalf("%s declares a Flux Kustomization that does not read as one: %v", rel, err)
			}
			if !strings.HasPrefix(doc.APIVersion, "kustomize.toolkit.fluxcd.io/") {
				continue
			}
			l := fluxLayer{File: rel, Name: doc.Metadata.Name, Namespace: doc.Metadata.Namespace, Substitute: doc.Spec.PostBuild.Substitute, SubstituteFrom: doc.Spec.PostBuild.SubstituteFrom}
			if p := strings.TrimPrefix(doc.Spec.Path, "./"); strings.Contains(p, "${") {
				l.dirs = sites
			} else {
				l.dirs = []string{p}
			}
			out = append(out, l)
		}
	}
	return out
}

// StandIns is a KEY=value listing, as validation substitutes it.
func StandIns(listing string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(listing, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if k = strings.TrimSpace(k); ok && k != "" && !strings.HasPrefix(k, "#") {
			out[k] = strings.TrimSpace(v)
		}
	}
	return out
}
