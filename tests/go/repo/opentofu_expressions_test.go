package repo

import (
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"homelab/tests/parsed"
)

// templateParts is a string template's parts: each reference by its name,
// and each piece of text with a NUL in front of it, so the two are never
// mistaken for each other.
func templateParts(expr hclsyntax.Expression) ([]string, bool) {
	tmpl, ok := expr.(*hclsyntax.TemplateExpr)
	if !ok {
		return nil, false
	}
	var out []string
	for _, part := range tmpl.Parts {
		if ref, ok := part.(*hclsyntax.ScopeTraversalExpr); ok {
			out = append(out, traversalName(ref.Traversal))
			continue
		}
		v, ok := parsed.Literal(part)
		if !ok || v.IsNull() || v.Type() != cty.String {
			return nil, false
		}
		out = append(out, "\x00"+v.AsString())
	}
	return out, true
}

// isLiteral is whether an expression is the string given, written out.
func isLiteral(expr hclsyntax.Expression, want string) bool {
	v, ok := parsed.Literal(expr)
	return ok && !v.IsNull() && v.Type() == cty.String && v.AsString() == want
}
