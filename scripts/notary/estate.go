package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"homelab/details/console"
	"homelab/details/onepassword"
	"homelab/details/vaults"
)

// What the lawyer grants a site and the notary reads, and what the notary
// grants it: an item each in the site's -shared vault.
const (
	servedItem      = "served"
	namesField      = "names"
	certificateItem = "certificate"
	chainField      = "chain"
	keyField        = "key"
	expiresField    = "expires"
)

// accountKey is the key the issuer knows the operator by. Whoever holds it
// can be issued a certificate for any name the domain's records let them
// prove, so it is the estate's own and no site's.
var accountKey = onepassword.Ref{Vault: vaults.Estate, Item: "issuer", Field: "account_key"}

// Where the two things the notary is given are kept. The credential that
// changes the domain's records is the estate's own; the domain is in the
// vault every site reads, as a proof's is allowed to be.
var (
	recordsToken = onepassword.Ref{Vault: vaults.Estate, Item: "access", Field: "api_token"}
	servedDomain = onepassword.Ref{Vault: vaults.EstateShared, Item: "poc", Field: "domain"}
)

// issue is the verb: every site the estate has granted names to holds a
// current certificate for exactly those names when it returns.
func issue(ctx context.Context, rehearse bool) error {
	console.Phase("Read", "What each site serves under, and what the operator is known by.")
	if !onepassword.Available() {
		return onepassword.ErrNoCLI
	}
	if !onepassword.SignedIn() {
		return errors.New("1Password is not signed in. The notary runs on the estate's service account: export " + onepassword.TokenVariable + " from the estate token")
	}
	held, err := onepassword.Vaults()
	if err != nil {
		return err
	}
	if err := vaults.CheckLawyer(held); err != nil {
		return err
	}
	// Which sites are owed one, before anything is asked of anybody: a run
	// that finds every certificate current reaches neither the issuer nor
	// the domain.
	now := time.Now()
	owed := map[string][]string{}
	for _, site := range vaults.SitesAmong(held) {
		shared := vaults.SiteShared(site)
		names := onepassword.Ref{Vault: shared, Item: servedItem, Field: namesField}
		if onepassword.Probe(names.String()) != onepassword.StatusOK {
			console.Info(site + " is granted no names, so it serves nothing a certificate is for")
			continue
		}
		granted, err := onepassword.Read(names.String())
		if err != nil {
			return err
		}
		want := strings.Fields(granted)
		chain := onepassword.Ref{Vault: shared, Item: certificateItem, Field: chainField}
		have := ""
		if onepassword.Probe(chain.String()) == onepassword.StatusOK {
			if have, err = onepassword.Read(chain.String()); err != nil {
				return err
			}
		}
		why, err := due(have, want, now)
		if err != nil {
			return fmt.Errorf("%s: %w", site, err)
		}
		if why == "" {
			console.Ok(site + "'s certificate is current")
			continue
		}
		console.Info(fmt.Sprintf("%s is owed a certificate for %d name(s): %s", site, len(want), why))
		owed[site] = want
	}
	if len(owed) == 0 {
		return nil
	}

	console.Phase("Issue", "Ask the issuer, proving each name by a record under the domain.")
	token, err := onepassword.Read(recordsToken.String())
	if err != nil {
		return err
	}
	domain, err := onepassword.Read(servedDomain.String())
	if err != nil {
		return err
	}
	zone, err := zoneOf(token, domain)
	if err != nil {
		return err
	}
	who, err := operator(ctx, rehearse)
	if err != nil {
		return err
	}
	if !rehearse {
		others, err := zone.bind(domain, who.uri)
		if err != nil {
			return err
		}
		console.Ok("the domain says only the operator's account may be issued a certificate under it")
		if others > 0 {
			console.Warn(fmt.Sprintf("%d other record(s) on the domain still let another party be issued one. They were not made here and are left alone: remove them by hand", others))
		}
	}

	sites := make([]string, 0, len(owed))
	for site := range owed {
		sites = append(sites, site)
	}
	sort.Strings(sites)
	for _, site := range sites {
		got, err := who.certificate(ctx, zone, owed[site])
		if err != nil {
			return fmt.Errorf("%s: %w", site, err)
		}
		days := int(got.expires.Sub(now).Hours() / 24)
		if rehearse {
			console.Ok(fmt.Sprintf("%s was issued a rehearsal certificate good for %d days. Nothing was written", site, days))
			continue
		}
		changed, err := onepassword.WriteItem(vaults.SiteShared(site), certificateItem, map[string]string{
			chainField:   got.chain,
			keyField:     got.key,
			expiresField: got.expires.UTC().Format(time.RFC3339),
		})
		if err != nil {
			return fmt.Errorf("granting %s its certificate: %w", site, err)
		}
		console.Ok(fmt.Sprintf("granted %s a certificate good for %d days: %s", site, days, strings.Join(changed, ", ")))
	}
	return nil
}
