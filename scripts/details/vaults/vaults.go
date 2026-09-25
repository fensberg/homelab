// Package vaults is the estate's one statement of which 1Password vault holds
// what, and which program may see which.
//
// One vault per scope, named for the scope, because a service account token
// is granted per vault and so the vault is the unit a program's reach can be
// drawn around:
//
//	estate          the lawyer's alone: account-wide credentials
//	estate-shared   the lawyer writes, every site reads
//	<site>-shared   the lawyer writes, that one site reads
//	<site>          the site's alone; the lawyer never sees it
//
// The names are generic enough to sit in git without naming anybody. Each
// program checks its token against these rules on every run, so the
// separation is enforced by what the token can see rather than by which
// template a program happens to render.
package vaults

import (
	"fmt"
	"slices"
	"strings"
)

// Estate holds the estate's valuable credentials, and only the lawyer sees it.
const Estate = "estate"

// EstateShared holds what every site needs from the estate and nobody is
// harmed by. The lawyer writes it; sites read it.
const EstateShared = "estate-shared"

const sharedSuffix = "-shared"

// SiteShared is the vault the lawyer writes for one site alone: what the
// estate grants that site.
func SiteShared(site string) string { return site + sharedSuffix }

// CheckLawyer holds the lawyer's token to the estate vault and the vaults it
// writes for others. A site's own vault is refused: what a site generates for
// itself is the site's, and the lawyer holding it would make a compromise of
// the estate a compromise of every site.
//
// Refused vaults are counted, never named: names reach a public Actions log.
func CheckLawyer(names []string) error {
	if !slices.Contains(names, Estate) {
		return fmt.Errorf("the 1Password token cannot see the %q vault. The lawyer's service account must be granted it", Estate)
	}
	refused := 0
	for _, n := range names {
		if n != Estate && !strings.HasSuffix(n, sharedSuffix) {
			refused++
		}
	}
	if refused > 0 {
		return fmt.Errorf("the 1Password token reaches %d vault(s) that are neither %q nor a %q vault. The lawyer holds the estate's credentials and writes what it shares; a site's own vault is the site's alone, so the service account must not be granted one", refused, Estate, "*"+sharedSuffix)
	}
	return nil
}

// CheckSite holds a site's token to its own vault and the two it reads: what
// the estate shares with every site, and what it grants this one. Anything
// else - the estate's own vault, another site's - is refused, because a site
// able to read it could harm the estate or a sibling.
func CheckSite(site string, names []string) error {
	if !slices.Contains(names, site) {
		return fmt.Errorf("the 1Password token cannot see the %q vault. This site's service account must be granted it", site)
	}
	allowed := []string{site, SiteShared(site), EstateShared}
	refused := 0
	for _, n := range names {
		if !slices.Contains(allowed, n) {
			refused++
		}
	}
	if refused > 0 {
		return fmt.Errorf("the 1Password token reaches %d vault(s) besides %q, %q and %q. A site reads its own secrets and what the estate shares with it, and nothing that could harm the estate or another site, so its service account must be granted those three alone", refused, site, SiteShared(site), EstateShared)
	}
	return nil
}
