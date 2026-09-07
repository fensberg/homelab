package main

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// How the clerk speaks to GitHub, and the one thing it must never be able to
// say.
//
// The clerk is an outside party. It reads a change and offers an opinion, and
// it has no business stopping anything. GitHub does not let that be expressed
// as a permission - commenting on a pull request and reviewing one both sit
// under Pull requests, so there is no grant meaning "may comment, may not
// approve". The guarantee therefore lives here, in one constant, with a test
// that fails if any other event string reaches the review endpoint, and a
// repository rule requiring code-owner review as the backstop.

// commentOnly is the only review event this program may ever send.
//
// APPROVE from a properly configured App counts toward branch protection - it
// is the standard Dependabot auto-approve trick - and REQUEST_CHANGES blocks
// the merge until a human dismisses it. Either would hand the outside party a
// lever. COMMENT is weightless by construction.
const commentOnly = "COMMENT"

// parseAppKey reads the App private key as it travels: base64 of the PEM.
//
// Base64 because the value passes through a JSON config and an environment
// variable, and a PEM's newlines survive neither reliably. That is a property
// of the journey rather than a security measure, which is why the estate's
// existing config validation says so in the same words.
func parseAppKey(encoded string) (*rsa.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("the private key is not base64. The downloaded .pem is stored base64-encoded on a single line")
	}

	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("the private key is valid base64 but is not a PEM block. Check that the encoded file was the .pem GitHub generated")
	}

	// GitHub issues PKCS#1; PKCS#8 is accepted so a key converted by openssl
	// still works rather than failing with something unhelpful.
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	any, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("the PEM is not an RSA private key")
	}
	k, ok := any.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("the PEM holds a %T, not an RSA private key", any)
	}
	return k, nil
}

// appJWT builds the assertion proving we hold the App's private key.
//
// Two details are load-bearing and neither fails visibly when wrong: base64url
// without padding, because standard base64 is rejected with an error saying
// only "bad credentials"; and an iat backdated for clock skew, because GitHub
// refuses a JWT issued even a second in its own future.
func appJWT(key *rsa.PrivateKey, appID string, now time.Time) (string, error) {
	b64 := func(v any) (string, error) {
		raw, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return base64.RawURLEncoding.EncodeToString(raw), nil
	}

	header, err := b64(map[string]string{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	claims, err := b64(map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": appID,
	})
	if err != nil {
		return "", err
	}

	signing := header + "." + claims
	digest := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("signing the app assertion: %w", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

type gh struct {
	api   string // base URL, so a test can point it somewhere hermetic
	repo  string // owner/name
	token string
	http  *http.Client
}

// grant is what an installation token actually carries.
//
// Read from the API rather than from a settings page, because a screenshot of
// intended permissions and the permissions a token holds are different facts,
// and only one of them decides what the program can do.
type grant map[string]string

// exchange turns the App's key into an installation token for this repository.
func exchange(api, repo, appID string, key *rsa.PrivateKey, client *http.Client, now time.Time) (*gh, grant, error) {
	assertion, err := appJWT(key, appID, now)
	if err != nil {
		return nil, nil, err
	}

	var install struct {
		ID int64 `json:"id"`
	}
	if err := call(client, http.MethodGet, api+"/repos/"+repo+"/installation", assertion, nil, &install); err != nil {
		return nil, nil, fmt.Errorf("finding the installation on %s: %w", repo, err)
	}

	var issued struct {
		Token       string `json:"token"`
		Permissions grant  `json:"permissions"`
	}
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", api, install.ID)
	if err := call(client, http.MethodPost, url, assertion, nil, &issued); err != nil {
		return nil, nil, fmt.Errorf("minting an installation token: %w", err)
	}
	if issued.Token == "" {
		return nil, nil, fmt.Errorf("GitHub issued no token and reported no error")
	}

	return &gh{api: api, repo: repo, token: issued.Token, http: client}, issued.Permissions, nil
}

// say posts the clerk's opinion as a review that cannot approve anything.
func (g *gh) say(pr int, body string) error {
	payload := map[string]string{"body": body, "event": commentOnly}
	url := fmt.Sprintf("%s/repos/%s/pulls/%d/reviews", g.api, g.repo, pr)
	return call(g.http, http.MethodPost, url, g.token, payload, nil)
}

// file opens an issue, for findings about the estate rather than about one
// change. Those outlive the pull request that surfaced them.
func (g *gh) file(title, body string) error {
	payload := map[string]string{"title": title, "body": body}
	url := fmt.Sprintf("%s/repos/%s/issues", g.api, g.repo)
	return call(g.http, http.MethodPost, url, g.token, payload, nil)
}

func call(client *http.Client, method, url, bearer string, send any, into any) error {
	var body io.Reader
	if send != nil {
		raw, err := json.Marshal(send)
		if err != nil {
			return fmt.Errorf("building the request: %w", err)
		}
		body = bytes.NewReader(raw)
	}

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return fmt.Errorf("building the request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if send != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %s", method, redactBearer(err.Error(), bearer))
	}
	defer func() { _ = resp.Body.Close() }()

	answer, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("reading the response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("GitHub answered %d: %s", resp.StatusCode, redactBearer(excerptOf(answer), bearer))
	}
	if into == nil {
		return nil
	}
	if err := json.Unmarshal(answer, into); err != nil {
		return fmt.Errorf("could not read the response: %w", err)
	}
	return nil
}

// A token is a credential with an hour to live, and this program's output
// lands in a public repository's Actions log. An hour is long enough.
func redactBearer(s, bearer string) string {
	if bearer == "" {
		return s
	}
	return strings.ReplaceAll(s, bearer, "[redacted]")
}

func excerptOf(body []byte) string {
	s := strings.TrimSpace(string(body))
	const max = 300
	if len(s) > max {
		return s[:max] + "..."
	}
	if s == "" {
		return "(no body)"
	}
	return s
}

// hunkHeader matches the new-side range of a unified diff hunk: the `+c,d` in
// `@@ -a,b +c,d @@`. A single-line hunk omits the count, which is why the
// second group is optional.
var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// shown reports which lines of which files this pull request actually displays.
//
// WHY THE CLERK NEEDS THIS AT ALL, which is not obvious and cost a run to find
// out. The clerk is handed the files a change touched and reads them WHOLE, so
// it routinely finds things on lines the change never went near. Those findings
// are uploaded, counted and opened as alerts exactly like any other - and then
// GitHub renders none of them on the pull request, because it only comments on
// alerts inside the diff. The finding exists, is correct SARIF, and is visible
// nowhere a reviewer is looking.
//
// That was survivable while the clerk also posted a count. It stopped being
// survivable when the count was removed: a run can now produce findings and
// give the reader no signal whatsoever, which is what happened on #277.
//
// The set is the hunk ranges rather than only the added lines, because GitHub
// shows context lines too and an alert anchored to one of those does render.
//
// A file with no `patch` - too large, or binary - yields no lines, so anything
// found in it is treated as unseen. That is the safe direction: the cost of
// being wrong is one comment too many, against a finding nobody ever reads.
func (g *gh) shown(pr int) (map[string]map[int]bool, error) {
	out := map[string]map[int]bool{}
	for page := 1; page <= 10; page++ {
		var files []struct {
			Filename string `json:"filename"`
			Patch    string `json:"patch"`
		}
		url := fmt.Sprintf("%s/repos/%s/pulls/%d/files?per_page=100&page=%d", g.api, g.repo, pr, page)
		if err := call(g.http, http.MethodGet, url, g.token, nil, &files); err != nil {
			return nil, fmt.Errorf("reading which lines #%d shows: %w", pr, err)
		}
		if len(files) == 0 {
			break
		}
		for _, f := range files {
			lines, ok := out[f.Filename]
			if !ok {
				lines = map[int]bool{}
				out[f.Filename] = lines
			}
			for line := range linesFromPatch(f.Patch) {
				lines[line] = true
			}
		}
		if len(files) < 100 {
			break
		}
	}
	return out, nil
}

// linesFromPatch reads the new-side line numbers a unified diff displays.
//
// Separated from shown so the parsing can be tested without a server, which is
// the half that decides whether a finding is treated as visible.
func linesFromPatch(patch string) map[int]bool {
	lines := map[int]bool{}
	body := strings.Split(patch, "\n")

	// A hunk header is metadata and can claim anything. "@@ -1,1 +1,4000000000
	// @@" is well formed, and trusting its count meant allocating four billion
	// map entries - the process was killed. This patch text comes from the
	// GitHub API, so it is not attacker-controlled in any interesting way, but
	// a wrong number in a header should cost nothing rather than everything.
	//
	// The bound is the patch's own length: a hunk cannot display more new-side
	// lines than the patch body contains, because each displayed line is one
	// line of it. Generous, exact enough, and needs no second source of truth.
	//
	// Found by FuzzLinesFromPatch. No table of hunk headers anybody writes by
	// hand contains this one.
	limit := len(body)

	for _, line := range body {
		m := hunkHeader.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		start, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		count := 1
		if m[2] != "" {
			if count, err = strconv.Atoi(m[2]); err != nil {
				continue
			}
		}
		if count > limit {
			count = limit
		}
		for i := start; i < start+count; i++ {
			// Files start at line 1. A hunk header of "@@ -0,0 +0,1 @@" is
			// well formed and yields line 0, and marking that shown claims a
			// finding there would render on the diff - about a line that does
			// not exist. Found by FuzzLinesFromPatch.
			if i < 1 {
				continue
			}
			lines[i] = true
		}
	}
	return lines
}
