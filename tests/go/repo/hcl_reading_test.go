package repo

import (
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// written is an attribute's value where it is written out, and null where
// it is absent or worked out from something else.
func written(body *hclsyntax.Body, name string) cty.Value {
	attr, has := body.Attributes[name]
	if !has {
		return cty.NullVal(cty.DynamicPseudoType)
	}
	v, diags := attr.Expr.Value(nil)
	if diags.HasErrors() {
		return cty.NullVal(cty.DynamicPseudoType)
	}
	return v
}
