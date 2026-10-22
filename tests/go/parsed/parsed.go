// Package parsed is the one way a guard reads the repository.
//
// WHY THIS EXISTS. A guard that reads a file as text and matches a pattern in
// it can be satisfied by a comment, misses a call split across two lines, and
// is a place nothing else knows it reads. The configuration rules read only
// through lib's selectors and the code rules through their paths, each place
// proved; a Go guard reads only through here. Every file comes back parsed by
// a parser for its kind, never as text, and where the repository is - its
// root - is known here and nowhere else, so a guard cannot reach a file some
// other way (agreed 2026-10-18). A guard's own temporary files are its own.
//
// A Tree is the repository, or a scratch copy of it that a meta test breaks:
// the mutation ledger points the guards at a copy through RootEnv, and every
// reader here honours it.
package parsed

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"gopkg.in/yaml.v3"

	"homelab/details/repopath"
)

// RootEnv names the tree the guards are judged against when it is not the
// repository: the scratch copy a meta test made.
const RootEnv = "HOMELAB_REPO_ROOT"

// fewestPlausibleFiles is fewer tracked files than this repository can have,
// so a listing below it is a broken listing, not a smaller tree.
const fewestPlausibleFiles = 100

// Tree is a checkout of the repository: the real one, or a scratch copy.
type Tree struct {
	root    string
	scratch bool
	// fixture is a tree a test made from nothing, holding only what it wrote.
	fixture bool
}

// Repository is the tree the guards are judged against: the scratch copy a
// meta test named in RootEnv, or else the checkout this package was built in.
func Repository(t testing.TB) *Tree {
	t.Helper()
	if override := os.Getenv(RootEnv); override != "" {
		if _, err := os.Stat(filepath.Join(override, "CLAUDE.md")); err != nil {
			t.Fatalf("%s is set to %s, which does not look like the repository: %v", RootEnv, override, err)
		}
		return &Tree{root: override, scratch: true}
	}
	root, err := repopath.Root()
	if err != nil {
		t.Fatal(err)
	}
	return &Tree{root: root}
}

func (r *Tree) path(rel string) string { return filepath.Join(r.root, filepath.FromSlash(rel)) }

// Files is every tracked file, from the repository's top. Tracked is the
// honest universe: .gitignore already says what is not the repository's.
func (r *Tree) Files(t testing.TB) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", r.root, "ls-files", "-z").Output()
	if err != nil {
		t.Fatalf("listing tracked files: %v", err)
	}
	var files []string
	for _, rel := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if rel != "" {
			files = append(files, rel)
		}
	}
	// A fixture holds only what its test wrote; the repository and copies of
	// it hold far more, and fewer means the listing broke.
	if !r.fixture && len(files) < fewestPlausibleFiles {
		t.Fatalf("git ls-files returned %d file(s), and this repository has many more; the listing has broken, and a guard reading it would pass by examining nothing", len(files))
	}
	return files
}

// Untracked is every file present and neither tracked nor ignored.
func (r *Tree) Untracked(t testing.TB) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", r.root, "ls-files", "-z", "--others", "--exclude-standard").Output()
	if err != nil {
		t.Fatalf("listing untracked files: %v", err)
	}
	var files []string
	for _, rel := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if rel != "" {
			files = append(files, rel)
		}
	}
	return files
}

// Exists reports whether anything is at a path.
func (r *Tree) Exists(rel string) bool {
	_, err := os.Lstat(r.path(rel))
	return err == nil
}

// IsDir reports whether a path is a directory.
func (r *Tree) IsDir(rel string) bool {
	info, err := os.Stat(r.path(rel))
	return err == nil && info.IsDir()
}

// Entries is the names in a directory, sorted.
func (r *Tree) Entries(t testing.TB, rel string) []string {
	t.Helper()
	entries, err := os.ReadDir(r.path(rel))
	if err != nil {
		t.Fatalf("reading the directory %s: %v", rel, err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func (r *Tree) read(t testing.TB, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(r.path(rel))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return b
}

// YAMLInto decodes the one document of a YAML file into v.
func (r *Tree) YAMLInto(t testing.TB, rel string, v any) {
	t.Helper()
	if err := yaml.Unmarshal(r.read(t, rel), v); err != nil {
		t.Fatalf("%s is not YAML that can be read: %v", rel, err)
	}
}

// YAMLDocuments is every document of a YAML file, decoded.
func (r *Tree) YAMLDocuments(t testing.TB, rel string) []any {
	t.Helper()
	dec := yaml.NewDecoder(bytes.NewReader(r.read(t, rel)))
	var docs []any
	for {
		var doc any
		err := dec.Decode(&doc)
		if err == io.EOF {
			return docs
		}
		if err != nil {
			t.Fatalf("%s is not YAML that can be read: %v", rel, err)
		}
		if doc != nil {
			docs = append(docs, doc)
		}
	}
}

// YAMLNode is a YAML file as its node tree, with every comment and position.
func (r *Tree) YAMLNode(t testing.TB, rel string) *yaml.Node {
	t.Helper()
	var node yaml.Node
	if err := yaml.Unmarshal(r.read(t, rel), &node); err != nil {
		t.Fatalf("%s is not YAML that can be read: %v", rel, err)
	}
	return &node
}

// JSONInto decodes a JSON file into v.
func (r *Tree) JSONInto(t testing.TB, rel string, v any) {
	t.Helper()
	if err := json.Unmarshal(r.read(t, rel), v); err != nil {
		t.Fatalf("%s is not JSON that can be read: %v", rel, err)
	}
}

// HCL is an HCL or OpenTofu file as HCL parses it.
func (r *Tree) HCL(t testing.TB, rel string) *hcl.File {
	t.Helper()
	f, diags := hclparse.NewParser().ParseHCL(r.read(t, rel), rel)
	if diags.HasErrors() {
		t.Fatalf("%s is not HCL that can be read: %s", rel, diags.Error())
	}
	return f
}

// Go is a Go file as Go's own parser reads it, comments kept.
func (r *Tree) Go(t testing.TB, fset *token.FileSet, rel string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(fset, rel, r.read(t, rel), parser.ParseComments)
	if err != nil {
		t.Fatalf("%s is not Go that can be read: %v", rel, err)
	}
	return f
}

// Rego is a policy file as OPA parses it: the whole syntax tree, locations
// kept, decoded.
func (r *Tree) Rego(t testing.TB, rel string) any {
	t.Helper()
	key := r.root + "\x00" + rel
	if v, ok := regoParses.Load(key); ok {
		return v
	}
	tree := r.parseRego(t, rel)
	regoParses.Store(key, tree)
	return tree
}

// regoParses is every policy file parsed already: a guard asks for the rules'
// trees once per proof, and each parse is a program run.
var regoParses sync.Map

func (r *Tree) parseRego(t testing.TB, rel string) any {
	t.Helper()
	if _, err := exec.LookPath("opa"); err != nil {
		t.Fatal("opa is not on PATH; take delivery of it: scripts/take-delivery.sh opa")
	}
	out, err := exec.Command("opa", "parse", "--format", "json", "--json-include", "locations", r.path(rel)).Output()
	if err != nil {
		t.Fatalf("opa could not parse %s: %v", rel, err)
	}
	var tree any
	if err := json.Unmarshal(out, &tree); err != nil {
		t.Fatalf("opa's syntax tree for %s could not be read: %v", rel, err)
	}
	return tree
}

// conftestParses is every parse asked for already, by file and parser: many
// guards read the same file, and each parse is a program run.
var conftestParses sync.Map

// Conftest is a file as Conftest's own parser for a kind reads it: an INI
// file's sections, an env file's keys, an ignore file's entries with its
// comments said to be comments, TOML's tables, a Dockerfile's instructions.
// The parsers of the engine the configuration rules run on, so a kind is read
// one way here and there.
func (r *Tree) Conftest(t testing.TB, rel, kind string) any {
	t.Helper()
	key := r.root + "\x00" + rel + "\x00" + kind
	if v, ok := conftestParses.Load(key); ok {
		return v
	}
	if _, err := exec.LookPath("conftest"); err != nil {
		t.Fatal("conftest is not on PATH; take delivery of it: scripts/take-delivery.sh conftest")
	}
	out, errs, err := r.Invoke(Run{}, "conftest", "parse", "--parser", kind, rel)
	if err != nil {
		t.Fatalf("conftest could not read %s as %s: %v\n%s", rel, kind, err, errs)
	}
	var parsed []struct {
		Contents any `json:"contents"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil || len(parsed) != 1 {
		t.Fatalf("conftest's reading of %s could not be read: %v", rel, err)
	}
	conftestParses.Store(key, parsed[0].Contents)
	return parsed[0].Contents
}

// Values is every string a parsed document holds, keys and values, in the
// order they were written where a parser keeps one. Never a comment, which no
// parser here hands over as a value.
func Values(doc any) []string {
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			out = append(out, v)
		case map[string]any:
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				out = append(out, k)
				walk(v[k])
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(doc)
	return out
}

// Run is a program run in the tree: in a directory of it, from the
// repository's top when dir is "", with the environment given beside the
// process's own. When judged is set it also runs judging this tree, as a guard
// run inside it does (RootEnv).
type Run struct {
	Dir    string
	Env    []string
	Stdin  string
	Judged bool
	// Outside runs the program in a directory of no tree's, so that a path
	// it is given relative to where it starts reaches nothing in one: how a
	// meta test runs the guards against a copy.
	Outside bool
}

// Invoke runs a program in the tree and gives what it wrote to standard
// output, what it wrote to standard error, and how it ended.
func (r *Tree) Invoke(run Run, name string, args ...string) (stdout, stderr []byte, err error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = r.path(run.Dir)
	if run.Outside {
		cmd.Dir = os.TempDir()
	}
	cmd.Env = append(os.Environ(), run.Env...)
	if run.Judged {
		cmd.Env = append(cmd.Env, RootEnv+"="+r.root)
	}
	if run.Stdin != "" {
		cmd.Stdin = strings.NewReader(run.Stdin)
	}
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	err = cmd.Run()
	return out.Bytes(), errs.Bytes(), err
}

// Copy is a scratch copy of the tree's tracked files, in a directory of the
// test's own, made a git repository so that it is listed as this one is.
func (r *Tree) Copy(t testing.TB) *Tree {
	t.Helper()
	dir := t.TempDir()
	for _, rel := range r.Files(t) {
		info, err := os.Lstat(r.path(rel))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue // tracked and deleted in the working tree: not there to copy
		case err != nil:
			t.Fatalf("reading %s to copy it: %v", rel, err)
		case !info.Mode().IsRegular():
			continue // a link or a submodule, which a copy of the files does not hold
		}
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, r.read(t, rel), info.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
	}
	copied := &Tree{root: dir, scratch: true}
	if _, errs, err := copied.Invoke(Run{}, "git", "init", "-q"); err != nil {
		t.Fatalf("git init in the scratch tree: %v\n%s", err, errs)
	}
	copied.Index(t)
	return copied
}

// Empty is a tree of the test's own, holding nothing yet: a fixture a test
// writes the files it means into, read the way the repository is read.
func Empty(t testing.TB) *Tree {
	t.Helper()
	tree := &Tree{root: t.TempDir(), scratch: true, fixture: true}
	if _, errs, err := tree.Invoke(Run{}, "git", "init", "-q"); err != nil {
		t.Fatalf("git init in a fixture tree: %v\n%s", err, errs)
	}
	return tree
}

// FixtureText is a fixture tree's file as text: what the test itself wrote,
// or what a change it made left. Never the repository's, nor a copy of it.
func (r *Tree) FixtureText(t testing.TB, rel string) string {
	t.Helper()
	if !r.fixture {
		t.Fatalf("asked for the text of %s in %s; text is given only for a fixture a test made", rel, r)
	}
	return string(r.read(t, rel))
}

// RemoveDocuments takes out of a scratch tree's YAML file every document drop
// picks, given it decoded, and leaves the others exactly as written: comments,
// order and all. A document that does not decode is never dropped.
func (r *Tree) RemoveDocuments(t testing.TB, rel string, drop func(doc any) bool) {
	t.Helper()
	r.mustBeScratch(t, "change")
	const separator = "\n---\n"
	body, lead := string(r.read(t, rel)), ""
	if strings.HasPrefix(body, "---\n") {
		lead, body = "---\n", strings.TrimPrefix(body, "---\n")
	}
	var kept []string
	for _, doc := range strings.Split(body, separator) {
		var decoded any
		if err := yaml.Unmarshal([]byte(doc), &decoded); err == nil && decoded != nil && drop(decoded) {
			continue
		}
		kept = append(kept, doc)
	}
	r.Write(t, rel, lead+strings.Join(kept, separator))
}

// Index records the scratch tree's files as tracked, so that a file planted
// or removed is listed as the change it is.
func (r *Tree) Index(t testing.TB) {
	t.Helper()
	r.mustBeScratch(t, "index")
	if _, errs, err := r.Invoke(Run{}, "git", "add", "-A", "-f"); err != nil {
		t.Fatalf("indexing the scratch tree: %v\n%s", err, errs)
	}
}

func (r *Tree) mustBeScratch(t testing.TB, what string) {
	t.Helper()
	if !r.scratch {
		t.Fatalf("asked to %s the repository itself; only a scratch copy is changed", what)
	}
}

// Replace changes the first place a scratch tree's file holds find, and says
// whether it held it.
func (r *Tree) Replace(t testing.TB, rel, find, replace string) bool {
	t.Helper()
	r.mustBeScratch(t, "change")
	body := string(r.read(t, rel))
	changed := strings.Replace(body, find, replace, 1)
	if changed == body {
		return false
	}
	r.Write(t, rel, changed)
	return true
}

// Write puts a file into a scratch tree.
func (r *Tree) Write(t testing.TB, rel, body string) {
	t.Helper()
	r.mustBeScratch(t, "write into")
	target := r.path(rel)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s: %v", rel, err)
	}
}

// Remove takes a file or a directory out of a scratch tree.
func (r *Tree) Remove(t testing.TB, rel string) {
	t.Helper()
	r.mustBeScratch(t, "remove from")
	if err := os.RemoveAll(r.path(rel)); err != nil {
		t.Fatalf("removing %s: %v", rel, err)
	}
}

// String names the tree for a message, without saying where it is.
func (r *Tree) String() string {
	if r.scratch {
		return "the scratch copy"
	}
	return "the repository"
}

var _ fmt.Stringer = (*Tree)(nil)
