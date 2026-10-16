package repo

import (
	"homelab/details/capacity"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

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
		if regexp.MustCompile(`(?m)^\s*chart:\s*gha-runner-scale-set\s*$`).MatchString(helmReleaseDocument(t, r.file, name)) {
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
func helmReleaseDocument(t *testing.T, rel, name string) string {
	t.Helper()
	for _, doc := range strings.Split(readRepoFile(t, rel), "\n---") {
		if regexp.MustCompile(`(?m)^kind:\s*HelmRelease\s*$`).MatchString(doc) &&
			regexp.MustCompile(`(?m)^  name:\s*`+regexp.QuoteMeta(name)+`\s*$`).MatchString(doc) {
			return doc
		}
	}
	t.Fatalf("%s does not declare the release %s", rel, name)
	return ""
}

// yamlScalar pulls one `key: value` scalar out of a manifest. Deliberately not
// a YAML parse: the file carries a ${VAR} Flux substitutes at reconcile time,
// which is not a thing a strict decode has to cope with just to read one field.
func yamlScalar(t *testing.T, body, key string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `:\s*(\S+)\s*$`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no %s: scalar in the scale set manifest", key)
	}
	return m[1]
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
