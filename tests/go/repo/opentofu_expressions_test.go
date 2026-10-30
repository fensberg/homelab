package repo

import (
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"homelab/tests/parsed"
)

// isLiteral is whether an expression is the string given, written out.
func isLiteral(expr hclsyntax.Expression, want string) bool {
	v, ok := parsed.Literal(expr)
	return ok && !v.IsNull() && v.Type() == cty.String && v.AsString() == want
}
