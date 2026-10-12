package main

import (
	"context"
	"errors"
	"fmt"
	"net"
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
	routesField     = "routes"
	underField      = "under"
	certificateItem = "certificate"
	chainField      = "chain"
	keyField        = "key"
	expiresField    = "expires"
	namesField      = "names"
)

// What is the operator's, in the operator's vault: the domain every name is
// under, the credential that changes its records, and the key the issuer
// knows the operator by. Whoever holds the last two can be issued a
// certificate for any name under the domain, so no estate and no site holds
// either.
var (
	servedDomain = onepassword.Ref{Vault: vaults.Operator, Item: certificateItem, Field: "domain"}
	recordsToken = onepassword.Ref{Vault: vaults.Operator, Item: certificateItem, Field: "api_token"}
	accountKey   = onepassword.Ref{Vault: vaults.Operator, Item: certificateItem, Field: "account_key"}
)

// served is what one site serves: each name, and the address it resolves to.
type served map[string]string

func (s served) names() []string {
	names := make([]string, 0, len(s))
	for name := range s {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// named turns what the estate granted a site into names. The estate says
// what is reached and where, and the label that sets the site apart when
// there is more than one; the domain is the operator's, so a name is made
// here and nowhere in an estate: <route>.<domain>, or
// <route>.<label>.<domain>.
func named(routes, under, domain string) (served, error) {
	out := served{}
	for _, each := range strings.Fields(routes) {
		route, address, ok := strings.Cut(each, "=")
		if !ok || route == "" || net.ParseIP(address) == nil {
			return nil, errors.New("what it is granted to serve is not a list of routes and their addresses")
		}
		name := route + "." + domain
		if under != "" {
			name = route + "." + under + "." + domain
		}
		out[name] = address
	}
	if len(out) == 0 {
		return nil, errors.New("it is granted routes to serve and the list is empty")
	}
	return out, nil
}

// issue is the verb: every site the estate has granted routes to has a name
// for each that resolves to it, and holds a current certificate for exactly
// those names, when it returns.
func issue(ctx context.Context, rehearse bool) error {
	console.Phase("Read", "What each site serves, and what is the operator's.")
	if !onepassword.Available() {
		return onepassword.ErrNoCLI
	}
	if !onepassword.SignedIn() {
		return errors.New("1Password is not signed in. The notary runs on a service account of its own: export " + onepassword.TokenVariable + " from the notary's token")
	}
	held, err := onepassword.Vaults()
	if err != nil {
		return err
	}
	if err := vaults.CheckNotary(held); err != nil {
		return err
	}
	domain, err := onepassword.Read(servedDomain.String())
	if err != nil {
		return err
	}

	now := time.Now()
	serves, owed := map[string]served{}, map[string]bool{}
	for _, site := range vaults.SitesAmong(held) {
		shared := vaults.SiteShared(site)
		routes := onepassword.Ref{Vault: shared, Item: servedItem, Field: routesField}
		if onepassword.Probe(routes.String()) != onepassword.StatusOK {
			console.Info(site + " is granted nothing to serve, so there is nothing a certificate is for")
			continue
		}
		granted, err := onepassword.Read(routes.String())
		if err != nil {
			return err
		}
		under := ""
		if label := (onepassword.Ref{Vault: shared, Item: servedItem, Field: underField}); onepassword.Probe(label.String()) == onepassword.StatusOK {
			if under, err = onepassword.Read(label.String()); err != nil {
				return err
			}
		}
		if serves[site], err = named(granted, under, domain); err != nil {
			return fmt.Errorf("%s: %w", site, err)
		}

		chain := onepassword.Ref{Vault: shared, Item: certificateItem, Field: chainField}
		have := ""
		if onepassword.Probe(chain.String()) == onepassword.StatusOK {
			if have, err = onepassword.Read(chain.String()); err != nil {
				return err
			}
		}
		why, err := due(have, serves[site].names(), now)
		if err != nil {
			return fmt.Errorf("%s: %w", site, err)
		}
		if why == "" {
			console.Ok(site + "'s certificate is current")
			continue
		}
		console.Info(fmt.Sprintf("%s is owed a certificate for %d name(s): %s", site, len(serves[site]), why))
		owed[site] = true
	}
	if len(serves) == 0 {
		return nil
	}
	sites := make([]string, 0, len(serves))
	for site := range serves {
		sites = append(sites, site)
	}
	sort.Strings(sites)

	console.Phase("Name", "Make each name resolve to what it names.")
	token, err := onepassword.Read(recordsToken.String())
	if err != nil {
		return err
	}
	zone, err := zoneOf(token, domain)
	if err != nil {
		return err
	}
	for _, site := range sites {
		made, removed, err := zone.publish(site, serves[site])
		if err != nil {
			return fmt.Errorf("%s: %w", site, err)
		}
		console.Ok(fmt.Sprintf("%s has a name for each of %d route(s): %d made or moved, %d of routes it no longer has removed", site, len(serves[site]), made, removed))
	}
	if len(owed) == 0 {
		return nil
	}

	console.Phase("Issue", "Ask the issuer, proving each name by a record under the domain.")
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
	for _, site := range sites {
		if !owed[site] {
			continue
		}
		names := serves[site].names()
		got, err := who.certificate(ctx, zone, names)
		if err != nil {
			return fmt.Errorf("%s: %w", site, err)
		}
		days := int(got.expires.Sub(now).Hours() / 24)
		if rehearse {
			console.Ok(fmt.Sprintf("%s was issued a rehearsal certificate good for %d days. It was not granted", site, days))
			continue
		}
		changed, err := onepassword.WriteItem(vaults.SiteShared(site), certificateItem, map[string]string{
			chainField:   got.chain,
			keyField:     got.key,
			expiresField: got.expires.UTC().Format(time.RFC3339),
			namesField:   strings.Join(names, " "),
		})
		if err != nil {
			return fmt.Errorf("granting %s its certificate: %w", site, err)
		}
		console.Ok(fmt.Sprintf("granted %s a certificate good for %d days: %s", site, days, strings.Join(changed, ", ")))
	}
	return nil
}
