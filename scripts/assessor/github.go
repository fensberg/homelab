package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"homelab/details/ghapi"
)

// client reads the GitHub API for one repository and keeps what it read, so
// a second run of the report asks only for what it has not seen.
type client struct {
	base  string // empty is GitHub itself
	repo  string
	token string
	cache string
}

// errGone is a log GitHub no longer keeps: past its retention, or never kept.
var errGone = fmt.Errorf("gone")

// get returns the body at a path under the repository, from the cache when
// it is there. A 404 or a 410 is errGone, and nothing else is forgiven.
func (c *client) get(path string) ([]byte, error) {
	sum := sha256.Sum256([]byte(path))
	file := filepath.Join(c.cache, hex.EncodeToString(sum[:16]))
	if body, err := os.ReadFile(file); err == nil {
		return body, nil
	}
	status, body, err := ghapi.Fetch(c.base, c.token, "/repos/"+c.repo+path)
	switch {
	case err != nil:
		return nil, fmt.Errorf("%s: %w", path, err)
	case status == http.StatusNotFound || status == http.StatusGone:
		return nil, errGone
	case status != http.StatusOK:
		return nil, fmt.Errorf("%s: GitHub answered %d", path, status)
	}
	if err := os.WriteFile(file, body, 0o600); err != nil {
		return nil, err
	}
	return body, nil
}

// pages reads every page of a listing. field names the array inside each
// page, or is empty when the page is the array. A listing shorter than the
// count GitHub declares for it is an error, never a smaller answer.
func pages[T any](c *client, path, field string) ([]T, error) {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	var all []T
	declared := -1
	for page := 1; ; page++ {
		body, err := c.get(fmt.Sprintf("%s%sper_page=100&page=%d", path, sep, page))
		if err != nil {
			return nil, err
		}
		var items []T
		if field == "" {
			err = json.Unmarshal(body, &items)
		} else {
			var wrapped map[string]json.RawMessage
			if err = json.Unmarshal(body, &wrapped); err == nil {
				err = json.Unmarshal(wrapped[field], &items)
			}
			if count, ok := wrapped["total_count"]; ok && err == nil {
				err = json.Unmarshal(count, &declared)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("%s page %d: %w", path, page, err)
		}
		all = append(all, items...)
		if len(items) < 100 {
			// GitHub stops some listings early and says nothing; the count it
			// declares is how a short answer is told from a whole one.
			if declared >= 0 && len(all) < declared {
				return nil, fmt.Errorf("%s: GitHub gave %d of the %d it counts", path, len(all), declared)
			}
			return all, nil
		}
	}
}

// each runs work over items, six at a time, and returns the first error.
func each[T any](items []T, work func(T) error) error {
	errs := make(chan error, len(items))
	gate := make(chan struct{}, 6)
	for _, item := range items {
		gate <- struct{}{}
		go func() {
			defer func() { <-gate }()
			errs <- work(item)
		}()
	}
	var first error
	for range items {
		if err := <-errs; err != nil && first == nil {
			first = err
		}
	}
	return first
}
