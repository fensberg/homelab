package repo

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// No code picks a site by name when it should have been told one.
//
// "site0" written as a value - a fallback in a workflow expression, a flag's
// default, a harness's answer when nobody said - quietly aims whatever did not
// name a site at the first one. That was true of the nightly, the contractor's
// -site, the taskfile and the test harness at once, and nothing showed it: with
// one site, the default and the only answer are the same. The day a second site
// exists, every one of them would have kept testing, recording and planning the
// first while reporting under names that did not say which.
//
// The sites come from the config template instead: the deploy and nightly
// matrices read its keys, and config.ResolveSite answers for a command that
// names none - the only site, or a refusal once there are two.
//
// Values only. A comment or a usage example naming a site is documentation,
// and prose that says "site0" hides nothing; so Go is checked by its string
// literals, through the parser rather than a regular expression, and the
// workflows and taskfile by the lines that are not comments.
var siteLiteral = regexp.MustCompile(`\bsite0\b`)

func TestNoCodePicksASiteByName(t *testing.T) {
	root := repoRoot(t)
	files := tracked(t, func(rel string) bool {
		switch {
		case strings.HasSuffix(rel, ".go"):
			return !strings.HasSuffix(rel, "_test.go")
		case strings.HasPrefix(rel, ".github/workflows/"):
			return strings.HasSuffix(rel, ".yml") || strings.HasSuffix(rel, ".yaml") || strings.HasSuffix(rel, ".js")
		default:
			return rel == "taskfile.yml"
		}
	})

	var found []string
	for _, rel := range files {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		found = append(found, siteValues(t, rel, body)...)
	}
	if len(files) < 20 {
		t.Fatalf("only %d files were read, so this checked almost nothing: the enumeration has stopped matching", len(files))
	}
	for _, f := range found {
		t.Errorf(`%s names a site as a value.

Take the site from the config template instead - a workflow from its sites
keys, as the deploy and nightly matrices do; a program from
config.ResolveSite, which gives the only site or refuses once there are two.
A default that names one site is the setting that goes on being right for
exactly one site.`, f)
	}
}

// siteValues finds the literal in a file's values: Go string literals, or the
// non-comment lines of anything else.
func siteValues(t *testing.T, rel string, body []byte) []string {
	t.Helper()
	var out []string
	if strings.HasSuffix(rel, ".go") {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, rel, body, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", rel, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if v, err := strconv.Unquote(lit.Value); err == nil && siteLiteral.MatchString(v) {
				out = append(out, fset.Position(lit.Pos()).String())
			}
			return true
		})
		return out
	}
	for i, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
			continue
		}
		if siteLiteral.MatchString(line) {
			out = append(out, rel+":"+strconv.Itoa(i+1))
		}
	}
	return out
}

// The reader finds the shapes that went wrong, and leaves the documentation.
func TestSiteValuesFindsValuesAndLeavesProse(t *testing.T) {
	cases := []struct {
		rel, body string
		want      int
	}{
		{"a.go", "package a\n// the first site, site0\nvar s = \"site0\"\n", 1},
		{"a.go", "package a\nconst u = `run: contractor plan -site site0`\n", 1},
		{"a.go", "package a\n// e.g. site0\nvar s = \"site10\"\n", 0},
		{"w.yml", "  # tests site0 nightly\n  SITE: ${{ inputs.site || 'site0' }}\n", 1},
		{"w.yml", "  SITE: ${{ matrix.site }}\n", 0},
		{"s.js", "// site0 only\nconst site = 'site0';\n", 1},
	}
	for _, tc := range cases {
		if got := len(siteValues(t, tc.rel, []byte(tc.body))); got != tc.want {
			t.Errorf("%s %q: found %d, want %d", tc.rel, tc.body, got, tc.want)
		}
	}
}
