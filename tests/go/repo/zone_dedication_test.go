package repo

import (
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"homelab/details/capacity"
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
	t.Parallel()
	const declared = "dmz_dedication"
	found := 0
	for _, file := range repository(t).OpenTofu(t) {
		path := file.Path
		for _, block := range file.Body.Blocks {
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
			// Kept for its zone, and closed to all work until it has its
			// clean snapshot: both, put on as the machine first joins.
			if want := capacity.UntrustedZone + "=somewhere:NoSchedule," + capacity.NotYetClean + "=true:NoSchedule"; kept.taint != want {
				t.Errorf("%s: a machine in the zone \"somewhere\" is tainted %q and not %q, so other work is not kept off it, or it takes work before it has a clean snapshot to be put back to", path, kept.taint, want)
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
	t.Parallel()
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
