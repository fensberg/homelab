package repo

import (
	"strings"
	"testing"
)

// The duplicate-key scanners read files nobody in this repository wrote.
//
// They are hand-written parsers over YAML, JSON and env-style text, and they
// decide whether a config silently declares the same key twice - a defect that
// is invisible in review and changes what the last declaration means. A parser
// that panics on a malformed file turns "this config is wrong" into "the check
// crashed", and a parser that reports a duplicate that is not there sends
// somebody to fix nothing.
//
// Both are properties over arbitrary input, which is what fuzzing is for and
// what a table of examples cannot reach.

func assertWellFormed(t *testing.T, kind, in string, dupes []Duplicate) {
	t.Helper()
	for _, d := range dupes {
		if d.Count < 2 {
			t.Errorf("%s reported %q appearing %d time(s) as a DUPLICATE for %q; below two it is not one", kind, d.Key, d.Count, in)
		}
	}
	// An empty key is deliberately NOT refused here. The first version of this
	// asserted every duplicate names something, and the fuzzer answered with
	// `{"":1,"":2}` - an object that declares the empty key twice, which is a
	// real duplicate and correctly reported. The assertion was wrong, not the
	// parser, and that is worth leaving written down: a fuzz target's first
	// job is often to correct the person who wrote it.
}

func FuzzYAMLDuplicates(f *testing.F) {
	for _, seed := range []string{
		"a: 1\nb: 2\n",
		"a: 1\na: 2\n",
		"top:\n  k: 1\n  k: 2\n",
		"- a: 1\n- a: 2\n",
		"", "---\n", "a: [1, 2\n", "\t\x00", "&a *a",
		"a: &x 1\nb: *x\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		dupes, err := YAMLDuplicates(in)
		if err != nil {
			// Refusing to parse is a fine answer for arbitrary bytes; claiming
			// findings while refusing is not.
			if len(dupes) != 0 {
				t.Errorf("YAMLDuplicates failed on %q and still returned %d finding(s)", in, len(dupes))
			}
			return
		}
		assertWellFormed(t, "YAMLDuplicates", in, dupes)
	})
}

func FuzzJSONDuplicates(f *testing.F) {
	for _, seed := range []string{
		`{"a":1,"b":2}`,
		`{"a":1,"a":2}`,
		`{"t":{"k":1,"k":2}}`,
		`[{"a":1},{"a":2}]`,
		"", "{", `{"a":}`, `{"":1,"":2}`, "null", "[[[[[[",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		dupes, err := JSONDuplicates(in)
		if err != nil {
			if len(dupes) != 0 {
				t.Errorf("JSONDuplicates failed on %q and still returned %d finding(s)", in, len(dupes))
			}
			return
		}
		assertWellFormed(t, "JSONDuplicates", in, dupes)
	})
}

func FuzzEnvDuplicates(f *testing.F) {
	for _, seed := range []string{
		"A=1\nB=2\n",
		"A=1\nA=2\n",
		"# comment\nA=1\n",
		"", "=", "A", "A=\nA=\n", "\x00=\x00",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		dupes := EnvDuplicates(in)
		assertWellFormed(t, "EnvDuplicates", in, dupes)
		// An env file has no nesting, so nothing may claim a path into one.
		for _, d := range dupes {
			if strings.Contains(d.Path, ".") {
				t.Errorf("EnvDuplicates reported a nested path %q for a flat format, on %q", d.Path, in)
			}
		}
	})
}
