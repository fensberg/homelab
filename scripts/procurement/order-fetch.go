// The fetch kind of delivery: a file taken from a URL - a release binary, an
// archive, an installer script - pinned by the SHA256 of exactly that file.
//
// ONE PATH FOR EVERY FILE. Ordering downloads the file and hashes it here,
// rather than trusting GitHub's asset digest for some and each publisher's
// checksum file for others. Three sources of truth would be three parsers and
// three ways to be wrong; this is one, and it is the same bytes take-delivery.sh
// will be handed.
//
// WHAT THIS DOES NOT PROVE. The hash is whatever the publisher served when this
// ran - the lock makes every later install match it, not match what the
// publisher intended. That is the same trust the SHA256 beside each version in
// versions.env always carried, now written where the installer must read it.
//
// A FILE WITH NO VERSION IN ITS URL. The OpenTofu installer script is served
// from one unversioned address and edited whenever OpenTofu likes. Its hash is
// locked anyway, by the operator's decision on #416: when OpenTofu edits it,
// take-delivery.sh refuses it until somebody orders again, and that refusal is
// the lock working rather than a fault.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// fetchFunc returns the SHA256 of the file served at a URL.
type fetchFunc func(url string) (string, error)

// fetchSections orders each fetched delivery.
func fetchSections(deliveries []declaredDelivery, fetch fetchFunc) ([]lockSection, error) {
	var out []lockSection
	for _, d := range deliveries {
		u, err := fetchURL(d)
		if err != nil {
			return nil, err
		}
		sum, err := fetch(u)
		if err != nil {
			return nil, fmt.Errorf("fetching %s for %s: %w", u, d.Name, err)
		}
		out = append(out, lockSection{
			Kind: "fetch", Name: d.Name, VersionKey: d.VersionKey, Version: d.Version,
			Lines: []string{u + " --hash=sha256:" + sum},
		})
	}
	return out, nil
}

// fetchURL puts the version into a fetch: template and refuses anything that
// is not a plain HTTPS address, because take-delivery.sh hands this line to
// curl and a lock is not the place to discover a scheme nobody meant.
func fetchURL(d declaredDelivery) (string, error) {
	u := strings.ReplaceAll(d.Spec, "{version}", d.Version)
	parsed, err := url.Parse(u)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || strings.ContainsAny(u, " \t{}") {
		return "", fmt.Errorf("the tools: entry for %s fetches %q, which is not an https URL once {version} is filled in", d.Source, d.Spec)
	}
	return u, nil
}

// fetchHash downloads a file and returns its SHA256.
func fetchHash(u string) (string, error) {
	// Minutes, not seconds: trufflehog's archive is tens of megabytes.
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the server answered %s", resp.Status)
	}
	h := sha256.New()
	n, err := io.Copy(h, resp.Body)
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", fmt.Errorf("the server sent an empty file, and an empty file's hash locks nothing")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
