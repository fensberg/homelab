package repo

import (
	"homelab/details/capacity"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/tests/parsed"
	"slices"
	"sort"
)

// The estate's runners. That their manifests agree with OpenTofu, that each
// job names its site, that they keep off the control plane and that none is
// BestEffort are configuration rules (policy/conftest/runners.rego). What is
// left here is how other guards find and read the runner sets.

// The runners' two Helm releases, which is what their manifests are found by.
const runnerScaleSet = "self-hosted"

// runnerSets is the release of every runner scale set the repository
// declares, found by the chart it installs. The runners are more than one
// set, split by the priority of their work, and a rule about "the runners"
// that read one of them by name would hold for that one and say nothing of
// the next.
func runnerSets(t *testing.T) []string {
	t.Helper()
	var sets []string
	for _, r := range whatReserves(t) {
		kind, name, _ := strings.Cut(r.object, "/")
		if kind != kindHelmRelease {
			continue
		}
		if chart, _ := scalarAt(helmReleaseDocument(t, r.file, name), "chart"); chart == "gha-runner-scale-set" {
			sets = append(sets, name)
		}
	}
	sort.Strings(sets)
	if !slices.Contains(sets, runnerScaleSet) {
		t.Fatalf("the runner scale sets found are %v, and the one every site's converge runs on (%s) is not among them", sets, runnerScaleSet)
	}
	return sets
}

// helmReleaseDocument is the one document of a manifest that declares the
// named release, so that two releases in one file are read apart.
func helmReleaseDocument(t *testing.T, rel, name string) any {
	t.Helper()
	for _, doc := range repository(t).YAMLDocuments(t, rel) {
		var d struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
		}
		if as(doc, &d) == nil && d.Kind == kindHelmRelease && d.Metadata.Name == name {
			return doc
		}
	}
	t.Fatalf("%s does not declare the release %s", rel, name)
	return nil
}

// yamlScalar is the first `key: value` scalar anywhere in a manifest's
// documents, and the test fails when there is none.
func yamlScalar(t *testing.T, docs []any, key string) string {
	t.Helper()
	if v, ok := scalarAt(docs, key); ok {
		return v
	}
	t.Fatalf("no %s: scalar in the scale set manifest", key)
	return ""
}

// scalarAt is the first string value under a key anywhere in parsed
// documents, keys walked in order so the answer does not depend on a map's.
func scalarAt(node any, key string) (string, bool) {
	switch v := node.(type) {
	case []any:
		for _, x := range v {
			if s, ok := scalarAt(x, key); ok {
				return s, true
			}
		}
	case map[string]any:
		if s, ok := v[key].(string); ok {
			return s, true
		}
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if s, ok := scalarAt(v[k], key); ok {
				return s, true
			}
		}
	}
	return "", false
}

// scalarsAt is every string value under a key anywhere in parsed documents,
// in the order scalarAt walks them.
func scalarsAt(node any, key string) []string {
	var out []string
	switch v := node.(type) {
	case []any:
		for _, x := range v {
			out = append(out, scalarsAt(x, key)...)
		}
	case map[string]any:
		if s, ok := v[key].(string); ok {
			out = append(out, s)
		}
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, scalarsAt(v[k], key)...)
		}
	}
	return out
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(b)
}

// --- CI stays off the machines holding quorum -------------------------------

// The affinity is evaluated rather than matched.
//
// A test asserting that the manifest contains the string "DoesNotExist" passes
// just as happily when the key is wrong, when the operator has been flipped to
// Exists, or when the whole term has been moved into the `preferred` block -
// where it excludes nothing at all. So this implements the small part of the
// scheduler's own rule that the property depends on, and asks it the two
// questions that matter: would this pod land on a control plane, and can it
// land anywhere.
//
// Terms are OR'd; the expressions inside one term are AND'd. That is the
// scheduler's semantics, and getting it backwards is exactly the mistake this
// is here to catch.
func nodeAffinityAdmits(t *testing.T, terms []any, labels map[string]string) bool {
	t.Helper()
	for _, term := range terms {
		m, ok := term.(map[string]any)
		if !ok {
			t.Fatalf("a nodeSelectorTerm is not a mapping: %#v", term)
		}
		exprs, ok := m[capacity.MatchExpressionsKey].([]any)
		if !ok {
			t.Fatalf("a nodeSelectorTerm has no matchExpressions: %#v", m)
		}
		all := true
		for _, e := range exprs {
			em := e.(map[string]any)
			key, _ := em["key"].(string)
			op, _ := em["operator"].(string)
			_, present := labels[key]
			switch op {
			case "Exists":
				all = all && present
			case capacity.HasNoLabel:
				all = all && !present
			default:
				// Refused rather than ignored. An operator this evaluator does
				// not model would otherwise be silently treated as a pass,
				// which is the failure mode the whole test exists to avoid.
				t.Fatalf("this test does not model the %q operator; teach it before using one", op)
			}
		}
		if all {
			return true
		}
	}
	return false
}

// versionPins is where every version the estate takes is pinned.
const versionPins = "scripts/versions.env"

// pinned is a version scripts/versions.env pins, read as the env file it is.
func pinned(t *testing.T, name string) string {
	t.Helper()
	v, ok := versionsPinned(t)[name]
	if !ok || strings.TrimSpace(v) == "" {
		t.Fatalf("%s declares no %s.\n\nEvery version this estate takes is pinned in that one file.", versionPins, name)
	}
	return strings.TrimSpace(v)
}

// versionsPinned is every version scripts/versions.env pins, by key, as
// Conftest's dotenv parser reads the file.
func versionsPinned(t *testing.T) map[string]string {
	t.Helper()
	declared, _ := repository(t).Conftest(t, versionPins, "dotenv").(map[string]any)
	pins := map[string]string{}
	for k, v := range declared {
		pins[k], _ = v.(string)
	}
	return pins
}

// header is the comment a rendered file opens with, as the YAML parser reads
// it: who rendered it, and from what. It is the subject of the checks that
// read it, so it is read where a parser keeps it and nowhere else.
func header(t *testing.T, rel string) string {
	t.Helper()
	node := repository(t).YAMLNode(t, rel)
	out := node.HeadComment
	if len(node.Content) > 0 {
		out += "\n" + node.Content[0].HeadComment
	}
	return out
}

// mappings is every document of a YAML file that is a mapping.
func mappings(t *testing.T, tree *parsed.Tree, rel string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, doc := range tree.YAMLDocuments(t, rel) {
		if m, ok := doc.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}
