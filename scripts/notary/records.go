package main

import (
	"fmt"
	"net/http"
	"net/url"

	cf "homelab/details/cloudflare"
)

// apiBase is a variable so the tests can point it at a local server.
var apiBase = cf.API

// zone is the domain's records, and the credential that changes them.
type zone struct{ token, id string }

type record struct {
	ID   string `json:"id"`
	Data struct {
		Tag   string `json:"tag"`
		Value string `json:"value"`
	} `json:"data"`
}

// zoneOf finds the domain's zone by its name. A token that cannot see it
// finds none, which is said as that and not as an empty answer.
func zoneOf(token, domain string) (zone, error) {
	found, err := cf.Get[[]struct {
		ID string `json:"id"`
	}](apiBase, token, "/zones", url.Values{"name": {domain}})
	if err != nil {
		return zone{}, err
	}
	if len(found) != 1 {
		return zone{}, fmt.Errorf("the token finds %d zone(s) for the domain certificates are issued under, and needs exactly one. It must be allowed to read that zone and edit its records", len(found))
	}
	return zone{token, found[0].ID}, nil
}

func (z zone) records() string { return "/zones/" + z.id + "/dns_records" }

// prove publishes what the issuer asks to find under a name, and returns
// what takes it away again. The record is the whole of the proof: whoever
// can make it is who the issuer takes to control the name.
func (z zone) prove(name, value string) (func() error, error) {
	made, err := cf.Send[record](http.MethodPost, apiBase, z.token, z.records(), map[string]any{
		"type": "TXT", "name": "_acme-challenge." + name, "content": `"` + value + `"`, "ttl": 60,
		"comment": "proves the name to the issuer; removed once it has looked",
	})
	if err != nil {
		return nil, err
	}
	return func() error {
		_, err := cf.Send[any](http.MethodDelete, apiBase, z.token, z.records()+"/"+made.ID, nil)
		return err
	}, nil
}

// bind makes the domain say that one account at one issuer, and nobody else,
// may be issued a certificate under it. The issuer checks this record before
// it issues, whoever is asking and however they proved a name, so it is what
// makes holding the account's key - and not merely reaching the records for
// a minute - the thing that gets a certificate.
//
// It adds the record when it is not there and changes nothing else. Any
// other record that permits an issuer is counted and returned: it was not
// made here, removing it could stop something of the operator's, and while
// it stands the binding is not the whole truth.
func (z zone) bind(domain, account string) (others int, err error) {
	held, err := cf.Get[[]record](apiBase, z.token, z.records(), url.Values{"type": {"CAA"}, "name": {domain}})
	if err != nil {
		return 0, err
	}
	want := issuerName + "; accounturi=" + account
	bound := false
	for _, r := range held {
		switch {
		case r.Data.Tag == "issue" && r.Data.Value == want:
			bound = true
		case r.Data.Tag == "issue" || r.Data.Tag == "issuewild":
			others++
		}
	}
	if bound {
		return others, nil
	}
	_, err = cf.Send[record](http.MethodPost, apiBase, z.token, z.records(), map[string]any{
		"type": "CAA", "name": domain, "ttl": 300,
		"data":    map[string]any{"flags": 0, "tag": "issue", "value": want},
		"comment": "only the operator's account may be issued a certificate under this domain",
	})
	return others, err
}
