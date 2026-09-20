package repo

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every variable Flux substitutes has a stand-in, and every stand-in is used.
//
// `clusters/` carries `${VAR}` placeholders that Flux fills from secrets
// OpenTofu writes. CI substitutes stand-ins before validating, because a
// manifest holding the string "${STATE_DB_NODEPORT}" where a port belongs
// cannot be parsed, let alone checked.
//
// Both directions matter, and they fail differently.
//
// A manifest variable with no stand-in substitutes to the EMPTY STRING. The
// manifest still parses - `nodePort:` with nothing after it is valid YAML -
// so validation passes on a document that is not the one Flux will apply, and
// the real failure waits until a reconcile.
//
// A stand-in nobody uses is the other half: a value kept current for a
// variable that was renamed or removed, which reads as coverage that is not
// there.
//
// This does NOT check that the secret really carries the variable. That is
// OpenTofu's side and a different tier's question; this one is about the two
// files in git agreeing.
//
// Two exclusions, both about what Flux never touches. `clusters/bootstrap` is
// applied by OpenTofu with kubectl rather than reconciled, and the Cilium
// manifest in it contains `${BIN_PATH}` - a shell variable inside a container
// command, which must survive to the node exactly as written. And comments
// are ignored, because kustomize drops them before anything substitutes
// anything, so a `${VAR}` used as prose in a comment is not a variable.
var (
	fluxVariable   = regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]*)\}`)
	substitutionKV = regexp.MustCompile(`(?m)^([A-Z][A-Z0-9_]*)=`)
)

func TestEveryFluxSubstitutionHasAStandInAndTheReverse(t *testing.T) {
	root := repoRoot(t)

	declared := map[string]bool{}
	for _, m := range substitutionKV.FindAllStringSubmatch(readRepoFile(t, "tests/flux-substitutions.env"), -1) {
		declared[m[1]] = true
	}
	if len(declared) == 0 {
		t.Fatal("tests/flux-substitutions.env declares no stand-ins, so this checked nothing")
	}

	used := map[string][]string{}
	err := filepath.WalkDir(filepath.Join(root, "clusters"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !(strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml")) {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if strings.HasPrefix(rel, filepath.Join("clusters", "bootstrap")) {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			for _, m := range fluxVariable.FindAllStringSubmatch(line, -1) {
				used[m[1]] = append(used[m[1]], rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking clusters/: %v", err)
	}

	names := make([]string, 0, len(used))
	for name := range used {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if !declared[name] {
			t.Errorf("%s uses ${%s}, and tests/flux-substitutions.env has no stand-in for it.\n\n"+
				"It substitutes to the empty string, which usually still parses - so validation "+
				"passes on a manifest that is not the one Flux applies, and the failure waits for "+
				"a reconcile. Add a stand-in whose SHAPE matches the real value.",
				used[name][0], name)
		}
	}
	for name := range declared {
		if _, ok := used[name]; !ok {
			t.Errorf("tests/flux-substitutions.env declares %s, and no manifest under clusters/ uses it.\n\n"+
				"Either the variable was renamed or removed and this was left behind, or the manifest "+
				"that needed it never landed. A stand-in for nothing reads as coverage that does not exist.",
				name)
		}
	}
}
