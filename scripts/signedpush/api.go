package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const apiBase = "https://api.github.com"

type api struct {
	token string
	http  *http.Client
}

// do issues one API call. Errors carry the status and GitHub's message but
// never the request body, which on a commit call contains the whole tree and
// on a token call contains the assertion.
func (a *api) do(method, path string, body any, out any) error {
	var buf io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		buf = bytes.NewReader(raw)
	}

	req, err := http.NewRequest(method, apiBase+path, buf)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := a.http
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Message == "" {
			e.Message = strings.TrimSpace(string(raw))
		}
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, e.Message)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// installationToken exchanges the app assertion for a token scoped to this
// installation. It expires in an hour, which is the point: a leaked one is
// an hour of exposure rather than until somebody remembers to revoke it.
func installationToken(jwt string, httpc *http.Client) (string, error) {
	jwtAPI := &api{token: jwt, http: httpc}

	var installs []struct {
		ID int64 `json:"id"`
	}
	if err := jwtAPI.do(http.MethodGet, "/app/installations", nil, &installs); err != nil {
		return "", err
	}
	if len(installs) == 0 {
		return "", fmt.Errorf("the app is not installed on any account")
	}
	if len(installs) > 1 {
		return "", fmt.Errorf("the app has %d installations; this program assumes one", len(installs))
	}

	var tok struct {
		Token string `json:"token"`
	}
	path := fmt.Sprintf("/app/installations/%d/access_tokens", installs[0].ID)
	if err := jwtAPI.do(http.MethodPost, path, nil, &tok); err != nil {
		return "", err
	}
	if tok.Token == "" {
		return "", fmt.Errorf("no token in the installation response")
	}
	return tok.Token, nil
}

// createCommit is the whole reason this program exists.
//
// author, committer and signature are deliberately absent. GitHub signs a
// commit created by an app installation ONLY when it supplies none of them -
// send any one and the commit comes back unsigned, with no error to say why.
func (a *api) createCommit(owner, repo, message, tree string, parents []string) (string, error) {
	body := map[string]any{"message": message, "tree": tree, "parents": parents}
	var out struct {
		SHA          string `json:"sha"`
		Verification struct {
			Verified bool   `json:"verified"`
			Reason   string `json:"reason"`
		} `json:"verification"`
	}
	if err := a.do(http.MethodPost, fmt.Sprintf("/repos/%s/%s/git/commits", owner, repo), body, &out); err != nil {
		return "", err
	}
	if !out.Verification.Verified {
		// Not cosmetic: an unverified commit is the failure this program
		// exists to prevent, and it is silent everywhere else.
		return "", fmt.Errorf("GitHub returned an UNVERIFIED commit (%s). The request carried author, committer or signature data, or the token is not an app installation token", out.Verification.Reason)
	}
	return out.SHA, nil
}

func (a *api) refSHA(owner, repo, ref string) (string, bool, error) {
	var out struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	err := a.do(http.MethodGet, fmt.Sprintf("/repos/%s/%s/git/ref/%s", owner, repo, ref), nil, &out)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return "", false, nil
		}
		return "", false, err
	}
	return out.Object.SHA, true, nil
}

// setRef creates the branch if it is new and fast-forwards it if it exists.
//
// force is never set. A force update is what `non_fast_forward` refuses, and
// wanting one here means the local branch was rewritten - which is a thing to
// notice, not to push through.
func (a *api) setRef(owner, repo, ref, sha string, exists bool) error {
	if exists {
		return a.do(http.MethodPatch, fmt.Sprintf("/repos/%s/%s/git/refs/%s", owner, repo, ref),
			map[string]any{"sha": sha, "force": false}, nil)
	}
	return a.do(http.MethodPost, fmt.Sprintf("/repos/%s/%s/git/refs", owner, repo),
		map[string]any{"ref": "refs/" + ref, "sha": sha}, nil)
}

func (a *api) deleteRef(owner, repo, ref string) error {
	return a.do(http.MethodDelete, fmt.Sprintf("/repos/%s/%s/git/refs/%s", owner, repo, ref), nil, nil)
}
