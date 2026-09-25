// Package cloudflare is the estate's one reading of Cloudflare's API: where
// it is, the envelope every answer arrives in, and where an account's object
// storage answers. The lawyer asks it about the estate, the contractor about
// a site, and the api tier about both, so none of them can drift from what
// the others believe Cloudflare says.
package cloudflare

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// API is the version of the API every caller here speaks.
const API = "https://api.cloudflare.com/client/v4"

// R2Endpoint is where an account's object storage answers the S3 protocol.
func R2Endpoint(accountID string) string {
	return "https://" + accountID + ".r2.cloudflarestorage.com"
}

// Answer is the envelope around every response. Success is the vendor's own
// verdict, and it is checked alongside the status code: an answer can be
// shaped like a success and still say it was not one.
type Answer[T any] struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result T `json:"result"`
}

// Get asks base+path with a bearer token and returns the result. A refusal is
// an error naming Cloudflare's own reason, never an empty result: an empty
// list read out of a 403 is an answer somebody will act on.
func Get[T any](base, token, path string, q url.Values) (T, error) {
	var zero T
	u := base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return zero, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return zero, fmt.Errorf("asking Cloudflare for %s: %w", path, err)
	}
	defer resp.Body.Close()
	var a Answer[T]
	if err := json.NewDecoder(resp.Body).Decode(&a); err != nil {
		return zero, fmt.Errorf("Cloudflare answered %d to %s with something that is not its envelope: %w", resp.StatusCode, path, err)
	}
	if resp.StatusCode != http.StatusOK || !a.Success {
		reason := "no reason given"
		if len(a.Errors) > 0 {
			reason = fmt.Sprintf("%d %s", a.Errors[0].Code, a.Errors[0].Message)
		}
		return zero, fmt.Errorf("Cloudflare refused %s (%d): %s", path, resp.StatusCode, reason)
	}
	return a.Result, nil
}
