package parsed

import (
	"slices"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// estate is a fixture of two roots and a test's fixture, the way the
// repository lays OpenTofu out.
func estate(t *testing.T) *Tree {
	t.Helper()
	tree := Empty(t)
	tree.Write(t, "ground/first.tf", `
locals {
  machine = {
    cores  = 8
    memory = 14336
  }
  purposes = toset(["state", "backups"])
}

resource "vm" "zone" {
  for_each = local.zones
  cpu {
    cores = local.machine.cores
  }
  memory {
    dedicated = local.machine.memory
  }
  # dedicated = 9999 is a comment, and no attribute
}

resource "vm" "worker" {
  memory {
    dedicated = 8192
  }
}
`)
	tree.Write(t, "ground/second.tf", `locals {
  zones = {}
}
`)
	tree.Write(t, "sky/third.tf", `resource "vm" "worker" {
  memory { dedicated = 4096 }
}
`)
	tree.Write(t, "ground/tests/fixture.tf", `resource "vm" "zone" {}`)
	tree.Write(t, "ground/notes.txt", `resource "vm" "zone" {}`)
	tree.Index(t)
	return tree
}

// What is read is every tracked OpenTofu file outside a test's fixtures, in
// path order; a text file that looks like OpenTofu is not one.
func TestOpenTofuIsEveryAuthoredFileAndNoTestsFixture(t *testing.T) {
	var got []string
	for _, f := range estate(t).OpenTofu(t) {
		got = append(got, f.Path)
	}
	if want := []string{"ground/first.tf", "ground/second.tf", "sky/third.tf"}; !slices.Equal(got, want) {
		t.Errorf("read %v, want %v", got, want)
	}
}

// A block is found by its kind and its labels' beginning; the one that
// declares something is found only when exactly one does.
func TestABlockIsFoundByWhatItDeclaresAndOnlyWhenOneDoes(t *testing.T) {
	tree := estate(t)
	if got := len(tree.Blocks(t, "resource", "vm")); got != 3 {
		t.Errorf("%d vm resources were found, and three are declared", got)
	}
	if b := tree.Declaring(t, "resource", "vm", "zone"); b.File != "ground/first.tf" || b.Address() != "resource vm zone" || b.Dir() != "ground" {
		t.Errorf("the zone was found as %s in %s", b.Address(), b.File)
	}
	if !refuses(t, func(tb testing.TB) { tree.Declaring(tb, "resource", "vm", "worker") }) {
		t.Error("a block declared in two roots was handed back as though it were one")
	}
	if !refuses(t, func(tb testing.TB) { tree.Declaring(tb, "resource", "vm", "gone") }) {
		t.Error("a block declared nowhere was not refused")
	}
}

// An attribute is found through the blocks around it, its value read only
// when it is written out, and a reference to a local is followed to what the
// local says - in the root it is written in, which a comment does not reach.
func TestAnAttributeIsReadThroughItsBlocksAndALocalIsFollowed(t *testing.T) {
	tree := estate(t)
	worker := tree.Blocks(t, "resource", "vm", "worker")[0]
	if v, ok := Literal(Attribute(worker.Body, "memory", "dedicated").Expr); !ok || !v.Equals(cty.NumberIntVal(8192)).True() {
		t.Errorf("the worker's memory read as %v (%v)", v, ok)
	}
	zone := tree.Declaring(t, "resource", "vm", "zone")
	written := Attribute(zone.Body, "memory", "dedicated").Expr
	if _, ok := Literal(written); ok {
		t.Error("a reference to a local was read as a value written out")
	}
	if v, ok := Literal(tree.Resolve(t, zone.Dir(), written)); !ok || !v.Equals(cty.NumberIntVal(14336)).True() {
		t.Errorf("local.machine.memory resolved to %v (%v)", v, ok)
	}
	if Attribute(zone.Body, "disk", "size") != nil {
		t.Error("an attribute in a block that is not written was found")
	}
	if got := tree.Source(t, written); got != "local.machine.memory" {
		t.Errorf("the reference reads back as %q", got)
	}
}

// A set of strings is read through the conversion it is written in, and
// anything worked out is not read as a value at all.
func TestStringsAreReadThroughConversionsAndNothingWorkedOutIs(t *testing.T) {
	tree := estate(t)
	purposes, ok := Strings(tree.Local(t, "purposes").Expr)
	if !ok || !slices.Equal(purposes, []string{"state", "backups"}) {
		t.Errorf("the purposes read as %v (%v)", purposes, ok)
	}
	if _, ok := Strings(tree.Declaring(t, "resource", "vm", "zone").Body.Attributes["for_each"].Expr); ok {
		t.Error("local.zones was read as strings written out")
	}
	if !refuses(t, func(tb testing.TB) { tree.LocalIn(tb, "sky", "purposes") }) {
		t.Error("a local of another root was handed back")
	}
	if !refuses(t, func(tb testing.TB) { tree.Local(tb, "nowhere") }) {
		t.Error("a local nobody declares was handed back")
	}
}
