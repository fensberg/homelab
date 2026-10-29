package parsed

import (
	"encoding/json"
	"os"
	"path"
	"sort"
	"testing"

	"homelab/details/applications"
	"homelab/tests/harness"
)

// layered is a fixture tree holding a layer pointed at a site by a variable,
// a layer of its own, and an application no layer applies.
func layered(t *testing.T) *Tree {
	t.Helper()
	tree := Empty(t)
	site := path.Join(harness.FluxTree, "somewhere")
	tree.Write(t, path.Join(site, applications.Kustomization), "resources: [layers.yaml, settings.yaml]\n")
	tree.Write(t, path.Join(site, "layers.yaml"), `apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: whole
  namespace: flux-system
spec:
  path: ./${WHERE}
  postBuild:
    substituteFrom:
      - kind: Secret
        name: vars
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: own
  namespace: flux-system
spec:
  path: ./`+path.Join(site, "own")+`
  postBuild:
    substitute:
      INLINE: given
---
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
metadata:
  name: not-a-layer
`)
	tree.Write(t, path.Join(site, "settings.yaml"), "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\ndata:\n  where: ${SITE}\n")
	tree.Write(t, path.Join(site, "own", applications.Kustomization), "resources: [one.yaml]\n")
	tree.Write(t, path.Join(site, "own", "one.yaml"), "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: one\ndata:\n  said: ${INLINE}\n")
	app := path.Join(applications.Dir, "thing", "base")
	tree.Write(t, path.Join(app, applications.Kustomization), "resources: [claim.yaml]\n")
	tree.Write(t, path.Join(app, "claim.yaml"), "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: thing\n")
	tree.Index(t)
	return tree
}

func TestEveryLayerIsRenderedWithWhatItIsGiven(t *testing.T) {
	tree := layered(t)
	read := map[string]renderedLayer{}
	for _, file := range tree.RenderedLayers(t, map[string]string{"NAME_THING": "thing.site.example"}) {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var r renderedLayer
		if err := json.Unmarshal(body, &r); err != nil {
			t.Fatalf("%s is not a rendered layer: %v", file, err)
		}
		read[r.Layer.Name+" "+r.Layer.Path] = r
	}
	var got []string
	for k := range read {
		got = append(got, k)
	}
	sort.Strings(got)
	site := path.Join(harness.FluxTree, "somewhere")
	want := []string{" " + path.Join(applications.Dir, "thing", "base"), "own " + path.Join(site, "own"), "whole " + site}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("rendered %v, want %v: a layer by its own path, one pointed by a variable at each site, and an application no layer applies", got, want)
	}
	whole := read["whole "+site]
	if whole.Layer.Namespace != "flux-system" || len(whole.Layer.SubstituteFrom) != 1 || whole.Layer.SubstituteFrom[0].Name != "vars" {
		t.Errorf("the layer's header does not say what it substitutes from: %+v", whole.Layer)
	}
	if read["own "+path.Join(site, "own")].Layer.Substitute["INLINE"] != "given" {
		t.Errorf("the layer's own substitutions were not carried")
	}
	var docs []map[string]any
	if err := json.Unmarshal(whole.Documents, &docs); err != nil || len(docs) != 4 {
		t.Errorf("the site's layer rendered %d documents (%v), want what kustomize builds of its listing: four", len(docs), err)
	}
	for k, r := range read {
		if r.Layer.DerivedStandIns["NAME_THING"] != "thing.site.example" || len(r.Layer.DerivedStandIns) != 1 {
			t.Errorf("%s does not carry the stand-ins it was handed, and nothing else: %v", k, r.Layer.DerivedStandIns)
		}
	}
}

// A directory kustomize cannot build is handed over by its error, and the
// rest are still rendered.
func TestADirectoryThatDoesNotBuildIsHandedOverByItsError(t *testing.T) {
	tree := layered(t)
	tree.Write(t, path.Join(harness.FluxTree, "somewhere", "own", applications.Kustomization), "resources: [gone.yaml]\n")
	tree.Index(t)
	unbuilt, built := 0, 0
	for _, file := range tree.RenderedLayers(t, nil) {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var r renderedLayer
		if err := json.Unmarshal(body, &r); err != nil {
			t.Fatal(err)
		}
		switch {
		case r.Layer.Unbuilt != "" && r.Layer.Name == "own":
			unbuilt++
		case r.Layer.Unbuilt == "":
			built++
		}
	}
	if unbuilt != 1 || built != 2 {
		t.Errorf("%d layer(s) handed over unbuilt and %d built; want the one that cannot build by its error, and the other two rendered", unbuilt, built)
	}
}

func TestAListingIsReadAsStandIns(t *testing.T) {
	got := StandIns("A=1\n  B = x\n# C=2\nnot one\n=empty\n")
	if len(got) != 2 || got["A"] != "1" || got["B"] != "x" {
		t.Errorf("standIns = %v", got)
	}
}
