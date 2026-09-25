package main

import (
	"errors"
	"fmt"
	"net/url"

	cf "homelab/details/cloudflare"
)

// apiBase is a variable so the tests can point it at a local server.
var apiBase = cf.API

type cloudflare struct {
	account string
	token   string
}

// get asks about the estate's own account, and says what a refusal most
// likely means here.
func get[T any](c cloudflare, path string, q url.Values) (T, error) {
	v, err := cf.Get[T](apiBase+"/accounts/"+c.account, c.token, path, q)
	if err != nil {
		return v, fmt.Errorf("%w. The estate token may lack the permission this needs", err)
	}
	return v, nil
}

// enrollmentApp returns the id of the account's WARP enrollment application,
// or "" when there is none. Found by type, not by name: the name is whatever
// Cloudflare or a person last called it, and the type is what makes it the
// enrollment application.
func (c cloudflare) enrollmentApp() (string, error) {
	apps, err := get[[]struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}](c, "/access/apps", nil)
	if err != nil {
		return "", err
	}
	var found []string
	for _, a := range apps {
		if a.Type == "warp" {
			found = append(found, a.ID)
		}
	}
	switch len(found) {
	case 0:
		return "", nil
	case 1:
		return found[0], nil
	default:
		return "", errors.New("the account holds more than one WARP enrollment application, which Cloudflare is meant to refuse. Resolve it in the dashboard before the estate adopts one")
	}
}

// liveTunnels counts the account's tunnels that have not been deleted. Every
// tunnel in the account is a site's, so any at all means a site stands on the
// estate.
func (c cloudflare) liveTunnels() (int, error) {
	tunnels, err := get[[]struct {
		ID string `json:"id"`
	}](c, "/cfd_tunnel", url.Values{"is_deleted": {"false"}})
	if err != nil {
		return 0, err
	}
	return len(tunnels), nil
}
