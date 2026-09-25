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

// connectedTunnels counts the account's tunnels that a connector is serving
// right now. Every tunnel belongs to a site, and the estate creates them, so a
// tunnel existing says only that the estate granted a plot; a connected one
// says a site is standing on it.
func (c cloudflare) connectedTunnels() (int, error) {
	tunnels, err := get[[]struct {
		Status string `json:"status"`
	}](c, "/cfd_tunnel", url.Values{"is_deleted": {"false"}})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range tunnels {
		if t.Status == "healthy" || t.Status == "degraded" {
			n++
		}
	}
	return n, nil
}

// bucketNames lists every R2 bucket in the account.
func (c cloudflare) bucketNames() (map[string]bool, error) {
	answer, err := get[struct {
		Buckets []struct {
			Name string `json:"name"`
		} `json:"buckets"`
	}](c, "/r2/buckets", url.Values{"per_page": {"1000"}})
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, b := range answer.Buckets {
		names[b.Name] = true
	}
	return names, nil
}
