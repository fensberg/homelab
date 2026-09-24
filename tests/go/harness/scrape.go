package harness

import (
	"encoding/json"
	"fmt"
	"sort"
)

// ScrapeFailures reads Prometheus's own account of why a job's targets are
// failing, from the body of GET /api/v1/targets?state=active.
//
// WHY THIS EXISTS. A test that finds `up == 0` knows that a scrape failed and
// not why, and the first version of the control-plane test filled that gap
// with a guess - "either the machine configuration was never converged, or
// the scrape is being refused". Prometheus had already recorded the actual
// error for every target, one request away. Guessing sent the investigation to
// an operator running commands by hand, which is the manual step this
// estate's tests exist to remove.
//
// So the rule this implements: a test checking a system that records its own
// errors reports that error, not a list of possibilities.
//
// Returns one line per unhealthy target - URL, health, and the error
// Prometheus recorded - and how many targets the job has in total, because
// "no targets at all" and "targets that fail" are different faults with
// different fixes, and a report that cannot tell them apart is a guess again.
func ScrapeFailures(body []byte, job string) (failures []string, total int, err error) {
	var answer struct {
		Status string `json:"status"`
		Data   struct {
			ActiveTargets []struct {
				Labels    map[string]string `json:"labels"`
				ScrapeURL string            `json:"scrapeUrl"`
				Health    string            `json:"health"`
				LastError string            `json:"lastError"`
			} `json:"activeTargets"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return nil, 0, fmt.Errorf("decoding Prometheus's target list: %w", err)
	}
	if answer.Status != "success" {
		return nil, 0, fmt.Errorf("Prometheus answered the target list with status %q", answer.Status)
	}

	for _, target := range answer.Data.ActiveTargets {
		if target.Labels["job"] != job {
			continue
		}
		total++
		if target.Health == "up" {
			continue
		}
		reason := target.LastError
		if reason == "" {
			// Health other than up with no recorded error is "unknown": the
			// target has not been scraped yet. Saying so beats an empty field
			// that reads as "no error".
			reason = "no error recorded yet - the target has not been scraped"
		}
		failures = append(failures, fmt.Sprintf("%s  %s  %s", target.ScrapeURL, target.Health, reason))
	}
	sort.Strings(failures)
	return failures, total, nil
}
