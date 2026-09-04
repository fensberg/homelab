package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// The seam between this program and whichever model answers.
//
// One function, deliberately. The vault field is `llm_key` and the supplier
// group is `independent-review`, both named for the job rather than the
// vendor, and this file is the third place that choice has to hold: changing
// which party answers should cost an endpoint, a pin, and the shape of one
// request - not a rewrite. There is no plugin interface here because two
// implementations is not a framework.

// errNoAnswer is returned when the call succeeded and said nothing.
//
// It is a distinct error rather than an empty string because an empty account
// and "no account was produced" must never be the same value. A model that
// declines, or a safety filter, or a response shape this code does not
// understand all arrive here, and reporting any of them as a finding-free run
// is the blind spot this estate refuses everywhere else.
var errNoAnswer = errors.New("the model returned no answer")

type asker struct {
	endpoint string // base URL, so a test can point it somewhere hermetic
	model    string
	key      string
	http     *http.Client
	attempts int
	backoff  time.Duration
}

// ask puts one question and returns the text of the answer.
func (a *asker) ask(prompt string) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"contents": []any{
			map[string]any{"parts": []any{map[string]string{"text": prompt}}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("building the request: %w", err)
	}

	url := a.endpoint + "/v1beta/models/" + a.model + ":generateContent"

	var last error
	for attempt := 1; attempt <= a.attempts; attempt++ {
		body, status, err := a.post(url, payload)
		switch {
		case err != nil:
			last = err
		case status == http.StatusTooManyRequests || status >= 500:
			// Worth waiting on: a quota window closes and a 5xx passes.
			last = fmt.Errorf("%s answered %d: %s", a.model, status, a.excerpt(body))
		case status != http.StatusOK:
			// Not worth waiting on. A rejected key is rejected on the third
			// attempt too, and retrying it burns a metered runner while
			// burying the cause under whatever the last attempt said.
			return "", fmt.Errorf("%s refused with %d: %s", a.model, status, a.excerpt(body))
		default:
			return a.textOf(body)
		}

		if attempt < a.attempts {
			time.Sleep(a.backoff * time.Duration(attempt))
		}
	}

	// The count is part of the fact. "It failed" and "it failed three times"
	// are different things to whoever reads the log.
	return "", fmt.Errorf("gave up after %d attempts: %w", a.attempts, last)
}

func (a *asker) post(url string, payload []byte) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, fmt.Errorf("building the request: %w", err)
	}
	req.Header.Set("content-type", "application/json")

	// A header, never a query parameter. The API accepts `?key=`, and that
	// form puts the credential in the request line - which reaches access
	// logs, proxy traces and any error that quotes the URL. This program's
	// output lands in a public repository's Actions log.
	req.Header.Set("x-goog-api-key", a.key)

	resp, err := a.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("calling the model: %s", a.redact(err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("reading the answer: %s", a.redact(err.Error()))
	}
	return body, resp.StatusCode, nil
}

func (a *asker) textOf(body []byte) (string, error) {
	var answer struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return "", fmt.Errorf("could not read the answer: %s: %s", a.redact(err.Error()), a.excerpt(body))
	}

	var b strings.Builder
	for _, c := range answer.Candidates {
		for _, p := range c.Content.Parts {
			b.WriteString(p.Text)
		}
	}
	if strings.TrimSpace(b.String()) == "" {
		return "", errNoAnswer
	}
	return b.String(), nil
}

// excerpt makes a response body safe and short enough to put in an error.
//
// Both halves matter. Dropping the body entirely would throw away the only
// diagnostic the vendor gave; quoting it whole would put an HTML error page in
// a job summary, and would publish the key on any path where the vendor echoes
// it back.
func (a *asker) excerpt(body []byte) string {
	s := a.redact(strings.TrimSpace(string(body)))
	const max = 300
	if len(s) > max {
		return s[:max] + "..."
	}
	if s == "" {
		return "(no body)"
	}
	return s
}

// redact removes the key from anything about to be printed.
//
// Scrubbing rather than suppressing, for the same reason: the diagnostic is
// worth keeping and the credential is not worth publishing.
func (a *asker) redact(s string) string {
	if a.key == "" {
		return s
	}
	return strings.ReplaceAll(s, a.key, "[redacted]")
}
