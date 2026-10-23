package parsed

import (
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// OpenTofuFile is one OpenTofu file the repository authored, parsed.
type OpenTofuFile struct {
	Path string
	Body *hclsyntax.Body
}

// Block is a top-level block of an OpenTofu file - a resource, a data
// source, a module call, a locals - and the file it is in.
type Block struct {
	File string
	*hclsyntax.Block
}

// Dir is the directory of the root or module the block is in.
func (b Block) Dir() string { return path.Dir(b.File) }

// Address is the block as it is named in a message: its kind and labels.
func (b Block) Address() string {
	return strings.Join(append([]string{b.Type}, b.Labels...), " ")
}

// Attr is an attribute of a locals block, and the file it is in.
type Attr struct {
	File string
	*hclsyntax.Attribute
}

var (
	tofuMu    sync.Mutex
	tofuCache = map[string][]OpenTofuFile{}
)

// OpenTofu is every OpenTofu file the repository authored, parsed, in path
// order: its roots and its modules, and none of their tests' fixtures. A file
// under a directory named tests is a test's input, not the estate.
//
// The repository's is parsed once for every guard that asks; a scratch tree
// is parsed each time, since a test changes it.
func (r *Tree) OpenTofu(t testing.TB) []OpenTofuFile {
	t.Helper()
	if !r.scratch {
		tofuMu.Lock()
		defer tofuMu.Unlock()
		if files, ok := tofuCache[r.root]; ok {
			return files
		}
	}
	var files []OpenTofuFile
	for _, rel := range r.Files(t) {
		if !strings.HasSuffix(rel, ".tf") || strings.Contains("/"+rel, "/tests/") {
			continue
		}
		body, ok := r.HCL(t, rel).Body.(*hclsyntax.Body)
		if !ok {
			t.Fatalf("%s is not OpenTofu's native syntax", rel)
		}
		files = append(files, OpenTofuFile{Path: rel, Body: body})
	}
	if len(files) == 0 && !r.fixture {
		t.Fatal("no tracked OpenTofu file was found, so there is nothing to check")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	if !r.scratch {
		tofuCache[r.root] = files
	}
	return files
}

// Blocks is every top-level block of a kind whose labels begin with the
// labels given, in every OpenTofu file the repository authored. With no
// labels, every block of the kind.
func (r *Tree) Blocks(t testing.TB, kind string, labels ...string) []Block {
	t.Helper()
	var out []Block
	for _, f := range r.OpenTofu(t) {
		for _, b := range f.Body.Blocks {
			if b.Type == kind && len(b.Labels) >= len(labels) && slices.Equal(b.Labels[:len(labels)], labels) {
				out = append(out, Block{File: f.Path, Block: b})
			}
		}
	}
	return out
}

// Declaring is the one block that declares something, wherever it is:
// resource "proxmox_virtual_environment_vm" "dmz", module "cluster". The test
// fails if none does or several do, so a guard never reads a block it did
// not mean.
func (r *Tree) Declaring(t testing.TB, kind string, labels ...string) Block {
	t.Helper()
	found := r.Blocks(t, kind, labels...)
	want := strings.Join(append([]string{kind}, labels...), " ")
	switch len(found) {
	case 1:
		return found[0]
	case 0:
		t.Fatalf("no OpenTofu file declares %s. If it was renamed or removed, the check that reads it has nothing to read", want)
	}
	var where []string
	for _, b := range found {
		where = append(where, b.File)
	}
	t.Fatalf("%s is declared %d times (%s), so there is no one to read. Ask for something only one of them declares", want, len(found), strings.Join(where, ", "))
	return Block{}
}

// Local is the one local value of that name in the repository, wherever it
// is declared. The test fails if none is, or several are.
func (r *Tree) Local(t testing.TB, name string) Attr {
	t.Helper()
	return r.localsNamed(t, name, func(string) bool { return true }, "the repository")
}

// LocalIn is the local value of that name in one root or module's
// directory: what a reference to it there means.
func (r *Tree) LocalIn(t testing.TB, dir, name string) Attr {
	t.Helper()
	return r.localsNamed(t, name, func(d string) bool { return d == dir }, dir)
}

func (r *Tree) localsNamed(t testing.TB, name string, in func(dir string) bool, where string) Attr {
	t.Helper()
	var found []Attr
	for _, b := range r.Blocks(t, "locals") {
		if !in(b.Dir()) {
			continue
		}
		if a, ok := b.Body.Attributes[name]; ok {
			found = append(found, Attr{File: b.File, Attribute: a})
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s declares local.%s %d times, and one was expected", where, name, len(found))
	}
	return found[0]
}

// Attribute is an attribute of a body, found through the nested blocks
// named before it: Attribute(body, "memory", "dedicated") is dedicated in the
// body's one memory block. Nil when it is not written, or when a block on
// the way is written more than once and so names no one attribute.
func Attribute(body *hclsyntax.Body, names ...string) *hclsyntax.Attribute {
	if len(names) == 0 {
		return nil
	}
	for _, name := range names[:len(names)-1] {
		var next *hclsyntax.Body
		for _, b := range body.Blocks {
			if b.Type != name {
				continue
			}
			if next != nil {
				return nil
			}
			next = b.Body
		}
		if next == nil {
			return nil
		}
		body = next
	}
	return body.Attributes[names[len(names)-1]]
}

// Literal is an expression's value when it is written out in full, with
// nothing it refers to and nothing it calls but the conversions tolist and
// toset, which are looked through: a string, a number, a list of strings, an
// object of them. False for anything that has to be worked out.
func Literal(expr hclsyntax.Expression) (cty.Value, bool) {
	expr = throughConversions(expr)
	if len(expr.Variables()) > 0 {
		return cty.NilVal, false
	}
	if hclsyntax.VisitAll(expr, func(n hclsyntax.Node) hcl.Diagnostics {
		if call, is := n.(*hclsyntax.FunctionCallExpr); is && !conversion[call.Name] {
			return hcl.Diagnostics{{Severity: hcl.DiagError}}
		}
		return nil
	}).HasErrors() {
		return cty.NilVal, false
	}
	v, diags := unconverted(expr).Value(nil)
	if diags.HasErrors() {
		return cty.NilVal, false
	}
	return v, true
}

// Strings is an expression's value as the strings it is written as: a
// string, or a list or set of them, in the order they are written. False for
// anything else, or anything that has to be worked out.
func Strings(expr hclsyntax.Expression) ([]string, bool) {
	v, ok := Literal(expr)
	if !ok || v.IsNull() {
		return nil, false
	}
	if v.Type() == cty.String {
		return []string{v.AsString()}, true
	}
	if !v.CanIterateElements() {
		return nil, false
	}
	var out []string
	for it := v.ElementIterator(); it.Next(); {
		_, e := it.Element()
		if e.IsNull() || e.Type() != cty.String {
			return nil, false
		}
		out = append(out, e.AsString())
	}
	return out, true
}

// Resolve is what an expression refers to when it is a local value or a
// path into one - local.machine, local.machine.memory - followed to the
// expression written there, in the directory given. Anything else is
// returned as it is.
func (r *Tree) Resolve(t testing.TB, dir string, expr hclsyntax.Expression) hclsyntax.Expression {
	t.Helper()
	ref, ok := expr.(*hclsyntax.ScopeTraversalExpr)
	if !ok || ref.Traversal.RootName() != "local" || len(ref.Traversal) < 2 {
		return expr
	}
	name, ok := ref.Traversal[1].(hcl.TraverseAttr)
	if !ok {
		return expr
	}
	at := r.LocalIn(t, dir, name.Name).Expr
	for _, step := range ref.Traversal[2:] {
		key, ok := step.(hcl.TraverseAttr)
		if !ok {
			return expr
		}
		next := Key(at, key.Name)
		if next == nil {
			t.Fatalf("%s: local.%s has no %s", dir, name.Name, key.Name)
		}
		at = next
	}
	return at
}

// Key is the expression written for a key of an object written out in
// full, or nil when the expression is no such object or has no such key. A
// key is written as a bare name or as a string; both are found.
func Key(expr hclsyntax.Expression, key string) hclsyntax.Expression {
	obj, ok := expr.(*hclsyntax.ObjectConsExpr)
	if !ok {
		return nil
	}
	for _, item := range obj.Items {
		if KeyName(item) == key {
			return item.ValueExpr
		}
	}
	return nil
}

// KeyName is an object item's key as it is written - a bare name, or a
// string - or empty when the key has to be worked out.
func KeyName(item hclsyntax.ObjectConsItem) string {
	k, ok := item.KeyExpr.(*hclsyntax.ObjectConsKeyExpr)
	if !ok {
		return ""
	}
	if name := hcl.ExprAsKeyword(k.Wrapped); name != "" {
		return name
	}
	if v, ok := Literal(k.Wrapped); ok && v.Type() == cty.String && !v.IsNull() {
		return v.AsString()
	}
	return ""
}

// Source is an expression as it is written in its file, for a message.
func (r *Tree) Source(t testing.TB, expr hclsyntax.Expression) string {
	t.Helper()
	rng := expr.Range()
	src := r.read(t, rng.Filename)
	if rng.End.Byte > len(src) || rng.Start.Byte > rng.End.Byte {
		return ""
	}
	return string(src[rng.Start.Byte:rng.End.Byte])
}

var conversion = map[string]bool{"tolist": true, "toset": true}

// throughConversions is the expression a tolist or toset call converts.
func throughConversions(expr hclsyntax.Expression) hclsyntax.Expression {
	for {
		call, ok := expr.(*hclsyntax.FunctionCallExpr)
		if !ok || !conversion[call.Name] || len(call.Args) != 1 {
			return expr
		}
		expr = call.Args[0]
	}
}

// unconverted is an expression with every conversion inside it looked
// through, so that it can be evaluated with no functions at all.
func unconverted(expr hclsyntax.Expression) hclsyntax.Expression {
	switch e := expr.(type) {
	case *hclsyntax.FunctionCallExpr:
		return unconverted(throughConversions(e))
	case *hclsyntax.TupleConsExpr:
		out := &hclsyntax.TupleConsExpr{SrcRange: e.SrcRange, OpenRange: e.OpenRange}
		for _, x := range e.Exprs {
			out.Exprs = append(out.Exprs, unconverted(x))
		}
		return out
	case *hclsyntax.ObjectConsExpr:
		out := &hclsyntax.ObjectConsExpr{SrcRange: e.SrcRange, OpenRange: e.OpenRange}
		for _, item := range e.Items {
			out.Items = append(out.Items, hclsyntax.ObjectConsItem{KeyExpr: item.KeyExpr, ValueExpr: unconverted(item.ValueExpr)})
		}
		return out
	}
	return expr
}
