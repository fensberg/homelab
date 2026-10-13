package repo

import (
	"homelab/details/capacity"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// A zone's machine carries its zone twice: as a taint, which keeps other
// work off it, and as a label, which is what the work meant for it selects.
//
// WHY BOTH. A toleration lets a pod onto a tainted machine and does not send
// it there. Work meant for an untrusted zone that could only tolerate would
// land wherever there was room, and the machines with room are the ones the
// estate trusts. A machine with the label and no taint is the other half of
// the same mistake: anything may land on it.
//
// EVALUATED, NOT READ. The declaration is found wherever it is, and worked
// out for a machine made up here, so what is held is what a machine is
// given and not how the line happens to be written.
func TestAZonesMachineIsKeptForItsZoneAndCanBeSentTo(t *testing.T) {
	const declared = "dmz_dedication"
	found := 0
	for path, body := range tofuSources(t) {
		file, diags := hclsyntax.ParseConfig([]byte(body), path, hcl.InitialPos)
		if diags.HasErrors() {
			t.Fatalf("%s does not parse: %s", path, diags.Error())
		}
		for _, block := range file.Body.(*hclsyntax.Body).Blocks {
			attr, has := block.Body.Attributes[declared]
			if block.Type != "locals" || !has {
				continue
			}
			found++
			kept, problem := dedicationOf(attr.Expr, "somewhere")
			if problem != "" {
				t.Errorf("%s: %s", path, problem)
				continue
			}
			if kept.taint != "untrusted-zone=somewhere:NoSchedule" {
				t.Errorf("%s: a machine in the zone \"somewhere\" is tainted %q, so other work is not kept off it", path, kept.taint)
			}
			if len(kept.labels) != 1 || kept.labels[capacity.UntrustedZone] != "somewhere" {
				t.Errorf("%s: a machine in the zone \"somewhere\" is labelled %v, so the work meant for it cannot be sent there", path, kept.labels)
			}
		}
	}
	if found == 0 {
		t.Fatalf("nothing declares %s, so nothing here says how a zone's machine is kept for its zone", declared)
	}
}

type dedication struct {
	taint  string
	labels map[string]string
}

// dedicationOf works the declaration out for one machine in the zone given.
func dedicationOf(expr hclsyntax.Expression, zone string) (dedication, string) {
	ctx := &hcl.EvalContext{Variables: map[string]cty.Value{
		"local": cty.ObjectVal(map[string]cty.Value{
			"dmz": cty.ObjectVal(map[string]cty.Value{
				"a-machine": cty.ObjectVal(map[string]cty.Value{"zone": cty.StringVal(zone)}),
			}),
		}),
	}}
	all, diags := expr.Value(ctx)
	if diags.HasErrors() {
		return dedication{}, "it cannot be worked out for a machine: " + diags.Error()
	}
	if !all.CanIterateElements() || all.LengthInt() != 1 {
		return dedication{}, "it does not give one answer for one machine"
	}
	one := all.AsValueMap()["a-machine"]
	if !one.Type().IsObjectType() || !one.Type().HasAttribute("taint") || !one.Type().HasAttribute("labels") {
		return dedication{}, "it does not say both a taint and labels"
	}
	out := dedication{labels: map[string]string{}}
	if v := one.GetAttr("taint"); v.Type() == cty.String && v.IsKnown() {
		out.taint = v.AsString()
	}
	if v := one.GetAttr("labels"); v.CanIterateElements() {
		for k, each := range v.AsValueMap() {
			if each.Type() == cty.String {
				out.labels[k] = each.AsString()
			}
		}
	}
	return out, ""
}

// The working out, on declarations made up here.
func TestADedicationIsWorkedOutAndAHalfOfOneIsSeen(t *testing.T) {
	for name, c := range map[string]struct {
		written string
		taint   string
		label   string
		problem bool
	}{
		"both":           {`{ for k, v in local.dmz : k => { taint = "kept=${v.zone}:NoSchedule", labels = { kept = v.zone } } }`, "kept=here:NoSchedule", "here", false},
		"a taint only":   {`{ for k, v in local.dmz : k => { taint = "kept=${v.zone}:NoSchedule" } }`, "", "", true},
		"labels only":    {`{ for k, v in local.dmz : k => { labels = { kept = v.zone } } }`, "", "", true},
		"another zone's": {`{ for k, v in local.dmz : k => { taint = "kept=elsewhere:NoSchedule", labels = { kept = "elsewhere" } } }`, "kept=elsewhere:NoSchedule", "elsewhere", false},
		"not worked out": {`{ for k, v in local.nothing : k => v }`, "", "", true},
	} {
		expr, diags := hclsyntax.ParseExpression([]byte(c.written), "made-up.tf", hcl.InitialPos)
		if diags.HasErrors() {
			t.Fatalf("%s: %s", name, diags.Error())
		}
		got, problem := dedicationOf(expr, "here")
		if (problem != "") != c.problem || got.taint != c.taint || got.labels["kept"] != c.label {
			t.Errorf("%s: got %+v, %q", name, got, problem)
		}
	}
}

// Every kind of machine is configured from the one list of what a machine
// is given.
//
// WHY. A zone's machine was configured from a list of its own, written
// beside the one the others use. What a machine needs was then added to
// that one - a hostname, a place for volumes, the labels that say where it
// is - and never to the zone's, because nothing said there were two. The
// first zone's machine built would have joined nameless and with nowhere
// to keep anything. One list cannot part from itself.
//
// EVERY KIND, wherever it is declared: each configuration the operating
// system's provider is asked to generate, not the kinds there were when
// this was written.
func TestEveryKindOfMachineIsConfiguredFromTheOneList(t *testing.T) {
	const generated, theList = "talos_machine_configuration", "machine_patches"
	kinds := 0
	for path, body := range tofuSources(t) {
		file, diags := hclsyntax.ParseConfig([]byte(body), path, hcl.InitialPos)
		if diags.HasErrors() {
			t.Fatalf("%s does not parse: %s", path, diags.Error())
		}
		for _, block := range file.Body.(*hclsyntax.Body).Blocks {
			if block.Type != "data" || len(block.Labels) != 2 || block.Labels[0] != generated {
				continue
			}
			kinds++
			if !drawsOn(block.Body.Attributes["config_patches"], theList) {
				t.Errorf("%s: %s.%s is not configured from local.%s. A kind of machine with a list of its own is given whatever that list had the day it was written, and none of what every machine has been given since.", path, generated, block.Labels[1], theList)
			}
		}
	}
	if kinds == 0 {
		t.Fatalf("no %s was found, so this checked nothing", generated)
	}
}

// drawsOn reports whether an attribute's value is worked out from the local
// named.
func drawsOn(attr *hclsyntax.Attribute, local string) bool {
	if attr == nil {
		return false
	}
	for _, ref := range attr.Expr.Variables() {
		if ref.RootName() != "local" || len(ref) < 2 {
			continue
		}
		if name, is := ref[1].(hcl.TraverseAttr); is && name.Name == local {
			return true
		}
	}
	return false
}

// The reading, on configurations made up here.
func TestAConfigurationThatDrawsOnTheListIsToldFromOneThatDoesNot(t *testing.T) {
	for name, c := range map[string]struct {
		written string
		want    bool
	}{
		"the list":            {`x = concat([local.other], local.the_list[each.key])`, true},
		"a list of its own":   {`x = concat([local.other], local.its_own[each.key])`, false},
		"written out":         {`x = ["a", "b"]`, false},
		"a variable so named": {`x = var.the_list`, false},
	} {
		file, diags := hclsyntax.ParseConfig([]byte(c.written), "made-up.tf", hcl.InitialPos)
		if diags.HasErrors() {
			t.Fatalf("%s: %s", name, diags.Error())
		}
		if got := drawsOn(file.Body.(*hclsyntax.Body).Attributes["x"], "the_list"); got != c.want {
			t.Errorf("%s: got %v", name, got)
		}
	}
	if drawsOn(nil, "the_list") {
		t.Error("a configuration with no patches at all was read as drawing on the list")
	}
}
