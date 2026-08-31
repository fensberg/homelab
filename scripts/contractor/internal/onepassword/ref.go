package onepassword

import (
	"fmt"
	"strings"
)

// Ref is an op:// reference broken into the parts `op item edit` addresses
// separately. Reading a secret only needs the whole string; writing one needs
// every piece, and putting a real credential in the wrong field is a failure
// that reports success.
type Ref struct {
	Vault   string
	Item    string
	Section string // empty when the field sits directly on the item
	Field   string
}

func (r Ref) String() string {
	if r.Section == "" {
		return fmt.Sprintf("op://%s/%s/%s", r.Vault, r.Item, r.Field)
	}
	return fmt.Sprintf("op://%s/%s/%s/%s", r.Vault, r.Item, r.Section, r.Field)
}

// ParseRef accepts op://vault/item/field and op://vault/item/section/field,
// which are the two shapes this project uses. Anything deeper is rejected
// rather than guessed at: op has no addressing for it, so a longer path is a
// typo and treating it as one is the only safe reading.
func ParseRef(ref string) (Ref, error) {
	rest, ok := strings.CutPrefix(ref, "op://")
	if !ok {
		return Ref{}, fmt.Errorf("%q is not an op:// reference", ref)
	}
	parts := strings.Split(rest, "/")
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			return Ref{}, fmt.Errorf("%q has an empty path segment", ref)
		}
	}
	switch len(parts) {
	case 3:
		return Ref{Vault: parts[0], Item: parts[1], Field: parts[2]}, nil
	case 4:
		return Ref{Vault: parts[0], Item: parts[1], Section: parts[2], Field: parts[3]}, nil
	default:
		return Ref{}, fmt.Errorf("%q has %d path segments; expected vault/item/field or vault/item/section/field", ref, len(parts))
	}
}
