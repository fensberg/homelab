package repo

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"gopkg.in/yaml.v3"

	"homelab/details/applications"
	"homelab/details/flux"
	"homelab/tests/parsed"
)

// Every layer Flux applies is given each variable it names.
//
// Flux fills a `${VAR}` from the secrets a layer says it substitutes from,
// and each layer names its own: the controllers are given the monitoring
// and storage secrets, the layer after them the site's addresses and names.
// A layer that names a variable none of its secrets carries is refused
// whole. Nothing in it is applied, everything behind it waits, and the
// manifests look right: validation fills every variable for every layer
// alike, so a variable in the wrong layer validates.
//
// That is what happened on 2026-10-12. A setting in the controllers layer
// named a variable that is in the next layer's secret, and the controllers
// layer stopped applying, with two new tools in it.
//
// So this reads both ends. What a layer names is every variable in the
// files it applies, found by following the layer's own path. What it is
// given is the keys of the secrets it lists, read from the OpenTofu that
// writes them: a key written out, or the beginning of a key made for each
// of something, like an address for each route.

// given is what one secret carries: keys written out, and the beginnings of
// keys made one for each of something.
type given struct {
	keys     map[string]bool
	prefixes []string
	// unread is why its keys could not all be read, or "".
	unread string
}

func (g given) carries(name string) bool {
	if g.keys[name] {
		return true
	}
	for _, p := range g.prefixes {
		if strings.HasPrefix(name, p) && len(name) > len(p) {
			return true
		}
	}
	return false
}

// secretKeysOf reads the keys of a secret's data out of the expression that makes
// it: an object written out, several merged, or one made by a loop whose key
// begins with words written out.
func secretKeysOf(expr hclsyntax.Expression, into *given) {
	switch e := expr.(type) {
	case *hclsyntax.ObjectConsExpr:
		for _, item := range e.Items {
			key, diags := item.KeyExpr.Value(nil)
			if diags.HasErrors() || key.Type() != cty.String {
				into.unread = "a key that is worked out and not written"
				continue
			}
			into.keys[key.AsString()] = true
		}
	case *hclsyntax.FunctionCallExpr:
		if e.Name != "merge" {
			into.unread = "data made by " + e.Name + "()"
			return
		}
		for _, arg := range e.Args {
			secretKeysOf(arg, into)
		}
	case *hclsyntax.ParenthesesExpr:
		secretKeysOf(e.Expression, into)
	case *hclsyntax.ForExpr:
		if tmpl, is := e.KeyExpr.(*hclsyntax.TemplateExpr); is && len(tmpl.Parts) > 1 {
			if first, diags := tmpl.Parts[0].Value(nil); !diags.HasErrors() && first.Type() == cty.String && first.AsString() != "" {
				into.prefixes = append(into.prefixes, first.AsString())
				return
			}
		}
		into.unread = "keys made by a loop that do not begin with words written out"
	default:
		into.unread = "data that is not an object written out"
	}
}

// secretsWritten is every Kubernetes Secret the OpenTofu here writes under a
// name written out, with what it carries.
func secretsWritten(files []parsed.OpenTofuFile) map[string]given {
	out := map[string]given{}
	for _, file := range files {
		for _, block := range file.Body.Blocks {
			if block.Type != "resource" || len(block.Labels) != 2 || block.Labels[0] != "kubernetes_secret" {
				continue
			}
			name := ""
			for _, inner := range block.Body.Blocks {
				if attr, has := inner.Body.Attributes["name"]; inner.Type == "metadata" && has {
					if v, diags := attr.Expr.Value(nil); !diags.HasErrors() && v.Type() == cty.String {
						name = v.AsString()
					}
				}
			}
			// A secret named by something worked out is not one a layer can
			// list by a name written in git, so it is not one of these.
			if name == "" {
				continue
			}
			g := given{keys: map[string]bool{}}
			if data, has := block.Body.Attributes["data"]; has {
				secretKeysOf(data.Expr, &g)
			}
			out[name] = g
		}
	}
	return out
}

// layer is one Flux Kustomization: where it is declared, what it applies,
// and the secrets it is given.
type layer struct {
	file, name, path string
	secrets          []string
	inline           map[string]bool
}

func layersIn(t testing.TB, rel string, docs []any) []layer {
	t.Helper()
	var out []layer
	for _, d := range docs {
		// A layer is a Flux Kustomization, and nothing else is read as one.
		if m, ok := d.(map[string]any); !ok || m["kind"] != flux.Kustomization {
			continue
		}
		var doc struct {
			APIVersion string `yaml:"apiVersion"`
			Kind       string `yaml:"kind"`
			Metadata   struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Spec struct {
				Path      string `yaml:"path"`
				PostBuild struct {
					Substitute     map[string]string `yaml:"substitute"`
					SubstituteFrom []struct {
						Kind string `yaml:"kind"`
						Name string `yaml:"name"`
					} `yaml:"substituteFrom"`
				} `yaml:"postBuild"`
			} `yaml:"spec"`
		}
		encoded, err := yaml.Marshal(d)
		if err == nil {
			err = yaml.Unmarshal(encoded, &doc)
		}
		if err != nil {
			t.Errorf("%s declares a Kustomization that is not one this reads, so what it is given was not checked: %v", rel, err)
			continue
		}
		if doc.Kind != flux.Kustomization || !strings.HasPrefix(doc.APIVersion, "kustomize.toolkit.fluxcd.io/") {
			continue
		}
		l := layer{file: rel, name: doc.Metadata.Name, path: strings.TrimPrefix(doc.Spec.Path, "./"), inline: map[string]bool{}}
		for k := range doc.Spec.PostBuild.Substitute {
			l.inline[k] = true
		}
		for _, from := range doc.Spec.PostBuild.SubstituteFrom {
			if from.Kind == "Secret" {
				l.secrets = append(l.secrets, from.Name)
			}
		}
		out = append(out, l)
	}
	return out
}

// applied is every manifest under a directory that a layer pointed at it
// applies: what its kustomization lists, followed into every directory it
// lists, or every manifest beneath it where it has none.
func applied(t testing.TB, tree *parsed.Tree, dir string, seen map[string]bool) ([]string, error) {
	t.Helper()
	if seen[dir] {
		return nil, nil
	}
	seen[dir] = true
	listingPath := dir + "/" + applications.Kustomization
	if !tree.Exists(listingPath) {
		var files []string
		for _, rel := range tree.Files(t) {
			if strings.HasPrefix(rel, dir+"/") && (strings.HasSuffix(rel, ".yaml") || strings.HasSuffix(rel, ".yml")) {
				files = append(files, rel)
			}
		}
		return files, nil
	}
	var k struct {
		Resources  []string `yaml:"resources"`
		Components []string `yaml:"components"`
		Patches    []struct {
			Path string `yaml:"path"`
		} `yaml:"patches"`
	}
	tree.YAMLInto(t, listingPath, &k)
	files := []string{listingPath}
	listed := append(append([]string{}, k.Resources...), k.Components...)
	for _, p := range k.Patches {
		if p.Path != "" {
			listed = append(listed, p.Path)
		}
	}
	for _, each := range listed {
		at := path.Join(dir, each)
		if !tree.Exists(at) {
			return nil, fmt.Errorf("%s lists %s, which is not there", listingPath, each)
		}
		if !tree.IsDir(at) {
			files = append(files, at)
			continue
		}
		under, err := applied(t, tree, at, seen)
		if err != nil {
			return nil, err
		}
		files = append(files, under...)
	}
	return files, nil
}

// named is every variable a manifest's values name. Flux substitutes into
// what kustomize built, which keeps no comment, so a comment names nothing.
func named(docs any) []string {
	var out []string
	for _, v := range parsed.Values(docs) {
		for _, m := range fluxVariable.FindAllStringSubmatch(v, -1) {
			out = append(out, m[1])
		}
	}
	return out
}

// notGiven is every variable a layer names that nothing it is given carries,
// each said once with the first manifest that names it.
func notGiven(l layer, names map[string][]string, secrets map[string]given) []string {
	var problems []string
	for _, s := range l.secrets {
		g, written := secrets[s]
		switch {
		case !written:
			problems = append(problems, fmt.Sprintf("%s: the layer %s is to be given the secret %s, and no OpenTofu here writes a secret of that name.", l.file, l.name, s))
		case g.unread != "":
			problems = append(problems, fmt.Sprintf("%s: the layer %s is given the secret %s, which has %s, so what it carries is not known here and nothing the layer names can be held to it.", l.file, l.name, s, g.unread))
		}
	}
	vars := make([]string, 0, len(names))
	for v := range names {
		vars = append(vars, v)
	}
	sort.Strings(vars)
	for _, v := range vars {
		carried := l.inline[v]
		for _, s := range l.secrets {
			carried = carried || secrets[s].carries(v)
		}
		if !carried {
			problems = append(problems, fmt.Sprintf("%s names ${%s}, and the layer that applies it, %s, is not given it.\n\n"+
				"The layer is given %s. Flux refuses a layer that names a variable it cannot fill: nothing in the layer is applied, and every layer behind it waits. "+
				"Use the variable in a layer that is given it, or give this layer the secret that carries it and say why beside it.",
				names[v][0], v, l.name, strings.Join(l.secrets, ", ")))
		}
	}
	return problems
}

func TestEveryLayerIsGivenEachVariableItNames(t *testing.T) {
	tree := repository(t)
	secrets := secretsWritten(tree.OpenTofu(t))

	var layers []layer
	for _, rel := range tracked(t, func(rel string) bool {
		return strings.HasPrefix(rel, fluxTree+"/") && (strings.HasSuffix(rel, ".yaml") || strings.HasSuffix(rel, ".yml"))
	}) {
		layers = append(layers, layersIn(t, rel, tree.YAMLDocuments(t, rel))...)
	}
	if len(layers) == 0 {
		t.Fatal("no layer was found in what Flux applies, so this checked nothing")
	}

	held := 0
	for _, l := range layers {
		// The layer every site begins from is pointed at a directory by a
		// variable of its own. What it applies is then each directory a
		// site can be pointed at: one with a listing at its top.
		dirs := []string{l.path}
		if strings.Contains(l.path, "${") {
			dirs = nil
			for _, e := range tree.Entries(t, fluxTree) {
				if dir := fluxTree + "/" + e; tree.IsDir(dir) && tree.Exists(dir+"/"+applications.Kustomization) {
					dirs = append(dirs, dir)
				}
			}
		}
		names := map[string][]string{}
		for _, v := range named(l.path) {
			names[v] = append(names[v], l.file)
		}
		for _, dir := range dirs {
			files, err := applied(t, tree, dir, map[string]bool{})
			if err != nil {
				t.Fatalf("%s: following what the layer %s applies: %v", l.file, l.name, err)
			}
			for _, rel := range files {
				for _, v := range named(tree.YAMLDocuments(t, rel)) {
					names[v] = append(names[v], rel)
				}
			}
		}
		if len(names) > 0 {
			held++
		}
		for _, problem := range notGiven(l, names, secrets) {
			t.Error(problem)
		}
	}
	if held == 0 {
		t.Fatal("no layer names a variable, so nothing was held to anything. The manifests here do name them, so the layers were not followed to their manifests.")
	}
}

// The reading of both ends is held to what it claims, against a secret and
// a layer written here.
func TestALayerIsHeldToWhatItsSecretsCarry(t *testing.T) {
	secrets := secretsWritten(tofuOf(t, map[string]string{"a.tf": `
resource "kubernetes_secret" "written" {
  metadata {
    name      = "written-out"
    namespace = "somewhere"
  }
  data = merge({
    for name, at in local.doors : "DOOR_${upper(name)}" => at
    }, {
    COLOUR = "green"
    SIZE   = local.size
  })
}

resource "kubernetes_secret" "worked_out" {
  metadata {
    name = "worked-out"
  }
  data = { for key, source in each.value : key => source }
}

resource "kubernetes_secret" "named_by_a_local" {
  metadata {
    name = local.name
  }
  data = { COLOUR = "red" }
}

resource "kubernetes_config_map" "not_a_secret" {
  metadata {
    name = "not-a-secret"
  }
  data = { COLOUR = "blue" }
}
`}))
	if len(secrets) != 2 {
		t.Fatalf("the secrets written under a name written out are two, and %d were read: %v", len(secrets), secrets)
	}
	written := secrets["written-out"]
	for name, want := range map[string]bool{"COLOUR": true, "SIZE": true, "DOOR_FRONT": true, "DOOR_": false, "WINDOW_FRONT": false, "colour": false} {
		if written.carries(name) != want {
			t.Errorf("whether the secret carries %s was read as %v", name, !want)
		}
	}
	if written.unread != "" || secrets["worked-out"].unread == "" {
		t.Errorf("a secret whose keys are written or begin with words is read whole, and one whose keys are worked out is said not to be; got %q and %q", written.unread, secrets["worked-out"].unread)
	}

	fixture := parsed.Empty(t)
	fixture.Write(t, "f.yaml", `
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata: {name: first}
spec:
  path: ./somewhere/first
  postBuild:
    substitute: {SHAPE: round}
    substituteFrom:
      - {kind: Secret, name: written-out}
      - {kind: ConfigMap, name: not-a-secret}
---
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources: [a.yaml]
`)
	fixture.Index(t)
	found := layersIn(t, "f.yaml", fixture.YAMLDocuments(t, "f.yaml"))
	if len(found) != 1 || found[0].path != "somewhere/first" || fmt.Sprint(found[0].secrets) != "[written-out]" || !found[0].inline["SHAPE"] {
		t.Fatalf("a Flux layer is read with its path and the secrets it is given, and a kustomize listing is not one; got %+v", found)
	}

	first := found[0]
	for name, c := range map[string]struct {
		l     layer
		names map[string][]string
		want  []string
	}{
		"everything it names is carried":  {first, map[string][]string{"COLOUR": {"x.yaml"}, "DOOR_BACK": {"x.yaml"}, "SHAPE": {"x.yaml"}}, nil},
		"a variable of another layer's":   {first, map[string][]string{"COLOUR": {"x.yaml"}, "WINDOW_FRONT": {"y.yaml"}}, []string{"y.yaml names ${WINDOW_FRONT}"}},
		"given a secret nothing writes":   {layer{file: "f.yaml", name: "second", secrets: []string{"nowhere"}}, nil, []string{"no OpenTofu here writes a secret of that name"}},
		"given a secret that is not read": {layer{file: "f.yaml", name: "third", secrets: []string{"worked-out"}}, map[string][]string{"COLOUR": {"x.yaml"}}, []string{"is not known here", "x.yaml names ${COLOUR}"}},
		"given nothing at all":            {layer{file: "f.yaml", name: "fourth"}, map[string][]string{"COLOUR": {"x.yaml"}}, []string{"x.yaml names ${COLOUR}"}},
	} {
		got := notGiven(c.l, c.names, secrets)
		if len(got) != len(c.want) {
			t.Errorf("%s: want %d problem(s), got %d: %v", name, len(c.want), len(got), got)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(strings.Join(got, "\n"), w) {
				t.Errorf("%s: no problem says %q: %v", name, w, got)
			}
		}
	}
	var manifest any
	if err := yaml.Unmarshal([]byte("a: ${ONE}\n# b: ${TWO}\nc: ${THREE} and ${one}\n"), &manifest); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(named(manifest)); got != "[ONE THREE]" {
		t.Errorf("the variables a manifest names, outside its comments, were read as %s", got)
	}
}
