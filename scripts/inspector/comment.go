package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Saying it where somebody is already looking.
//
// The tally's first version wrote to $GITHUB_STEP_SUMMARY, which is a page you
// reach by clicking into a run. On the pull request there was a green check
// named "What this change takes away" and nothing to read - so the argument
// for building it at all ("stated where somebody is already looking") was
// false of the thing built. On a change that did gut a guard it would have
// passed green with the removal recorded where nobody goes.
//
// Two rules follow, and both are about not becoming noise.
//
// Silent when there is nothing. Every push would otherwise carry "Nothing. No
// files removed" - fifty a day, restating what the diff already shows, until
// the line is skipped by reflex and takes the one that matters with it.
//
// One comment, edited. #160 is exactly the bug of appending one per push, and
// reproducing it here would be careless: a pull request with thirty of these
// is a pull request nobody reads.

// marker identifies this program's comment so it can be found and edited.
// Invisible in rendered Markdown, and specific enough not to match prose.
const marker = "<!-- inspector:tally -->"

type commenter struct {
	api   string
	repo  string
	token string
	http  *http.Client
}

func newCommenter(api, repo, token string) *commenter {
	return &commenter{api: api, repo: repo, token: token, http: &http.Client{Timeout: 30 * time.Second}}
}

// post puts the report on the pull request, editing its own previous comment
// rather than adding another.
//
// A report with nothing to say removes the old comment instead of leaving a
// stale one: "four test functions removed" is worse than useless once it is
// no longer true.
func (c *commenter) post(pr int, body string, worthSaying bool) error {
	existing, err := c.find(pr)
	if err != nil {
		return err
	}

	switch {
	case !worthSaying && existing == 0:
		return nil
	case !worthSaying:
		return c.call(http.MethodDelete, fmt.Sprintf("%s/repos/%s/issues/comments/%d", c.api, c.repo, existing), nil)
	case existing == 0:
		return c.call(http.MethodPost, fmt.Sprintf("%s/repos/%s/issues/%d/comments", c.api, c.repo, pr),
			map[string]string{"body": marker + "\n" + body})
	default:
		return c.call(http.MethodPatch, fmt.Sprintf("%s/repos/%s/issues/comments/%d", c.api, c.repo, existing),
			map[string]string{"body": marker + "\n" + body})
	}
}

// find returns the id of this program's own comment, or zero.
func (c *commenter) find(pr int) (int64, error) {
	var comments []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	url := fmt.Sprintf("%s/repos/%s/issues/%d/comments?per_page=100", c.api, c.repo, pr)
	if err := c.get(url, &comments); err != nil {
		return 0, err
	}
	for _, m := range comments {
		if strings.Contains(m.Body, marker) {
			return m.ID, nil
		}
	}
	return 0, nil
}

func (c *commenter) get(url string, into any) error {
	return c.do(http.MethodGet, url, nil, into)
}

func (c *commenter) call(method, url string, send any) error {
	return c.do(method, url, send, nil)
}

func (c *commenter) do(method, url string, send, into any) error {
	var body io.Reader
	if send != nil {
		raw, err := json.Marshal(send)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if send != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %s", method, url, redact(err.Error(), c.token))
	}
	defer func() { _ = resp.Body.Close() }()

	answer, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("GitHub answered %d for %s: %s", resp.StatusCode, url,
			redact(strings.TrimSpace(string(answer)), c.token))
	}
	if into == nil {
		return nil
	}
	return json.Unmarshal(answer, into)
}

// The token lives an hour and this output lands in a public repository's log.
func redact(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "[redacted]")
}

func env(name string) string { return strings.TrimSpace(os.Getenv(name)) }
