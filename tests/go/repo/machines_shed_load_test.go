package repo

import (
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"

	"homelab/details/capacity"
)

// How a machine sheds load is declared, for every kind of machine there is
// (docs/epochs/04-observability.md, criterion 16): what it holds back from
// pods for itself, and the free memory below which the kubelet puts work off
// it, gently and then at once.
//
// WHY. Left at the kubelet's defaults, a machine held back about half of
// what its own services were measured to use, and promised pods the rest.
// On a site run full that is where an eviction comes from that nothing
// declared.
//
// EVALUATED, NOT READ. The declarations are found wherever they are and
// worked out: the figures for each kind, which kind each machine is, and
// that what every machine is configured with reads both.
func TestEveryKindOfMachineDeclaresHowItShedsLoad(t *testing.T) {
	// The kubelet's own defaults on this operating system, which a
	// declaration that holds back less than would be no declaration at all.
	defaults := map[string]int64{"control_plane": 512 << 20, "worker": 384 << 20, "untrusted": 384 << 20}

	found := map[string]bool{}
	for path, body := range tofuSources(t) {
		file, diags := hclsyntax.ParseConfig([]byte(body), path, hcl.InitialPos)
		if diags.HasErrors() {
			t.Fatalf("%s does not parse: %s", path, diags.Error())
		}
		for _, block := range file.Body.(*hclsyntax.Body).Blocks {
			if block.Type != "locals" {
				continue
			}
			attrs := block.Body.Attributes
			if attr, ok := attrs["held_back"]; ok {
				found["held_back"] = true
				v, diags := attr.Expr.Value(nil)
				if diags.HasErrors() || !v.Type().IsObjectType() {
					t.Errorf("%s: held_back is not a declaration of figures: %s", path, diags.Error())
					v = cty.EmptyObjectVal
				}
				for kind, least := range defaults {
					if !v.Type().HasAttribute(kind) {
						t.Errorf("%s: held_back says nothing of the %s kind of machine, which then sheds load as the kubelet's defaults say", path, kind)
						continue
					}
					one := v.GetAttr(kind)
					size := func(field string) int64 {
						if !one.Type().IsObjectType() || !one.Type().HasAttribute(field) || one.GetAttr(field).Type() != cty.String {
							t.Errorf("%s: a %s machine's %s is not declared", path, kind, field)
							return 0
						}
						n, err := capacity.Bytes(one.GetAttr(field).AsString())
						if err != nil {
							t.Errorf("%s: a %s machine's %s is not a size: %v", path, kind, field, err)
						}
						return n
					}
					system, soft, hard := size("system"), size("soft"), size("hard")
					if system < least {
						t.Errorf("%s: a %s machine holds back %d MiB for itself, less than the kubelet's own default of %d", path, kind, system>>20, least>>20)
					}
					if soft <= hard {
						t.Errorf("%s: a %s machine's gentle threshold is not above its hard one, so work is never put off gently", path, kind)
					}
					if !one.Type().HasAttribute("grace") {
						t.Errorf("%s: a %s machine says how far it may go before work is put off gently, and not for how long", path, kind)
					}
				}
			}
			if attr, ok := attrs["kind_of_machine"]; ok {
				found["kind_of_machine"] = true
				ctx := &hcl.EvalContext{Variables: map[string]cty.Value{"local": cty.ObjectVal(map[string]cty.Value{
					"control_plane": cty.ObjectVal(map[string]cty.Value{"a-control-plane": cty.EmptyObjectVal}),
					"workers":       cty.ObjectVal(map[string]cty.Value{"a-worker": cty.EmptyObjectVal}),
					"dmz":           cty.ObjectVal(map[string]cty.Value{"an-untrusted-one": cty.EmptyObjectVal}),
				})}, Functions: map[string]function.Function{"merge": stdlib.MergeFunc, "keys": stdlib.KeysFunc}}
				v, diags := attr.Expr.Value(ctx)
				var got map[string]cty.Value
				if diags.HasErrors() || !v.CanIterateElements() {
					t.Errorf("%s: which kind each machine is cannot be worked out: %s", path, diags.Error())
				} else {
					got = v.AsValueMap()
				}
				for machine, kind := range map[string]string{"a-control-plane": "control_plane", "a-worker": "worker", "an-untrusted-one": "untrusted"} {
					if k, ok := got[machine]; !ok || k.Type() != cty.String || k.AsString() != kind {
						t.Errorf("%s: %s is not taken as the %s kind of machine, so it sheds load by no declaration", path, machine, kind)
					}
				}
			}
			if attr, ok := attrs["machine_patches"]; ok {
				found["machine_patches"] = true
				reads := map[string]bool{}
				for _, traversal := range attr.Expr.Variables() {
					if len(traversal) > 1 && traversal.RootName() == "local" {
						if name, ok := traversal[1].(hcl.TraverseAttr); ok {
							reads[name.Name] = true
						}
					}
				}
				for _, needed := range []string{"held_back", "kind_of_machine"} {
					if !reads[needed] {
						t.Errorf("%s: what every machine is configured with does not read %s, so what is declared reaches no machine", path, needed)
					}
				}
			}
		}
	}
	for _, name := range []string{"held_back", "kind_of_machine", "machine_patches"} {
		if !found[name] {
			t.Errorf("nothing declares %s, so how a machine sheds load is the kubelet's default", name)
		}
	}
}
