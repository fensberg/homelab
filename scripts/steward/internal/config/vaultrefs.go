package config

import "sort"

// VaultRef is one op:// reference in the config template, paired with the
// config path that holds it. Both halves matter in a report: the reference
// says what to fix in 1Password, the path says what breaks if you do not.
type VaultRef struct {
	ConfigPath string
	Ref        string
}

// VaultReferences lists every op:// reference the template declares.
//
// This is the template half of AssertRenderedConfigComplete, split out so it
// can be asked on its own. That function compares a template against an
// already-rendered config, so it can only speak after `op inject` has run and
// written secrets to disk. Listing the references is a question about the
// template alone - no vault, no credentials, no rendered file - which is what
// lets a preflight check the vault before a run commits to anything.
//
// Unwrapped references are included deliberately. `op inject` substitutes only
// the {{ }} form, so a bare op:// string travels into the rendered config
// verbatim; a preflight that ignored those would call a vault complete while
// the run was about to hand a provider the literal reference text.
func VaultReferences(templatePath string) ([]VaultRef, error) {
	leaves, err := flattenFile(templatePath)
	if err != nil {
		return nil, err
	}

	var refs []VaultRef
	for path, val := range leaves {
		s, ok := val.(string)
		if !ok {
			continue
		}
		// FindString returns the reference without the surrounding moustache
		// or whitespace, which is the form `op read` actually takes.
		if ref := opRefPattern.FindString(s); ref != "" {
			refs = append(refs, VaultRef{ConfigPath: path, Ref: ref})
		}
	}

	// flattenFile returns a map, and Go randomises map iteration - without
	// this the report would list the same vault in a different order on every
	// run, which makes two runs impossible to diff.
	sort.Slice(refs, func(i, j int) bool { return refs[i].ConfigPath < refs[j].ConfigPath })
	return refs, nil
}
