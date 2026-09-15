package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// attestation gates a pull request that touches a sensitive path on a review
// conversation somebody other than its author resolved.
//
// It runs from the Sensitive Paths workflow, which supplies the digest of the
// sensitive part of the diff. The digest is what binds an acknowledgement to
// content: change one of those files and the marker changes, no thread matches
// it, and a new conversation opens. An acknowledgement therefore cannot
// outlive what it acknowledged, which is the failure the label it replaced had
// by construction.
func main() {
	var (
		repo   = flag.String("repo", "", "owner/name")
		pr     = flag.Int("pr", 0, "pull request number")
		digest = flag.String("digest", "", "digest of the sensitive part of the diff")
		anchor = flag.String("anchor", "", "file the conversation hangs off")
		head   = flag.String("head", "", "head commit the conversation is anchored to")
		report = flag.String("report", "", "path to the body to post")
	)
	flag.Parse()

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" || *repo == "" || *pr == 0 || *digest == "" {
		fmt.Fprintln(os.Stderr, "attestation: need GITHUB_TOKEN, -repo, -pr and -digest")
		os.Exit(2)
	}
	c := &client{repo: *repo, token: token, http: &http.Client{Timeout: 30 * time.Second}}

	threads, author, err := c.threads(*pr)
	if err != nil {
		// Fail closed. Being unable to read the acknowledgement is not the
		// same as there being one.
		fmt.Fprintln(os.Stderr, "attestation: could not read the review conversations, so this "+
			"change cannot be shown to be acknowledged: "+err.Error())
		os.Exit(1)
	}

	marker := fmt.Sprintf("<!-- sensitive-attestation:%s -->", *digest)
	v := Decide(threads, marker, author)

	if v.OpenThread {
		body, err := os.ReadFile(*report)
		if err != nil {
			fmt.Fprintln(os.Stderr, "attestation: reading the report: "+err.Error())
			os.Exit(1)
		}
		if err := c.openThread(*pr, *head, *anchor, marker+"\n"+string(body)); err != nil {
			fmt.Fprintln(os.Stderr, "attestation: opening the conversation: "+err.Error())
			os.Exit(1)
		}
	}

	// Withdraw the flags nobody answered about content that is gone.
	//
	// After the live conversation is opened, so a failure here never leaves a
	// pull request with no current thread on it. Best effort for the same
	// reason: a superseded thread left behind is the situation this improves
	// on rather than a new failure, and refusing the whole gate over one would
	// be worse than the noise it removes.
	for _, id := range v.DeleteComments {
		if err := c.deleteComment(id); err != nil {
			fmt.Fprintln(os.Stderr, "attestation: could not withdraw a superseded "+
				"conversation, so it stays open and has to be resolved by hand: "+err.Error())
			continue
		}
		fmt.Printf("attestation: withdrew a superseded conversation (comment %d) - "+
			"it described a diff this pull request no longer carries, and nobody had answered it\n", id)
	}

	if v.Blocked {
		fmt.Fprintln(os.Stderr, "attestation: "+v.Reason)
		os.Exit(1)
	}
	fmt.Println("attestation: " + v.Reason)
}

type client struct {
	repo  string
	token string
	http  *http.Client
}

// apiBase is a variable so a test can point it somewhere hermetic. Nothing
// else reassigns it, and the same shape is used by every other program here
// that talks to GitHub.
var apiBase = "https://api.github.com"

const threadQuery = `query($owner:String!,$repo:String!,$number:Int!){
  repository(owner:$owner,name:$repo){
    pullRequest(number:$number){
      author{login}
      reviewThreads(first:100){nodes{
        isResolved
        resolvedBy{login __typename}
        comments(first:1){nodes{body databaseId}}
      }}
    }
  }
}`

func (c *client) threads(pr int) ([]Thread, string, error) {
	owner, name, err := split(c.repo)
	if err != nil {
		return nil, "", err
	}
	payload, _ := json.Marshal(map[string]any{
		"query":     threadQuery,
		"variables": map[string]any{"owner": owner, "repo": name, "number": pr},
	})
	raw, err := c.post(apiBase+"/graphql", payload)
	if err != nil {
		return nil, "", err
	}

	var out struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					Author struct {
						Login string `json:"login"`
					} `json:"author"`
					ReviewThreads struct {
						Nodes []struct {
							IsResolved bool `json:"isResolved"`
							ResolvedBy *struct {
								Login    string `json:"login"`
								TypeName string `json:"__typename"`
							} `json:"resolvedBy"`
							Comments struct {
								Nodes []struct {
									Body       string `json:"body"`
									DatabaseID int64  `json:"databaseId"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", err
	}
	if len(out.Errors) > 0 {
		return nil, "", fmt.Errorf("graphql: %s", out.Errors[0].Message)
	}

	p := out.Data.Repository.PullRequest
	var threads []Thread
	for _, n := range p.ReviewThreads.Nodes {
		t := Thread{Resolved: n.IsResolved}
		if n.ResolvedBy != nil {
			t.ResolvedBy = n.ResolvedBy.Login
			t.ResolvedByType = n.ResolvedBy.TypeName
		}
		if len(n.Comments.Nodes) > 0 {
			t.FirstCommentBody = n.Comments.Nodes[0].Body
			t.FirstCommentID = n.Comments.Nodes[0].DatabaseID
		}
		threads = append(threads, t)
	}
	return threads, p.Author.Login, nil
}

// openThread anchors the conversation to the file rather than to a line.
// GitHub's own security bot opens review conversations the same way, and a
// file-level anchor cannot be invalidated by the line moving.
func (c *client) openThread(pr int, head, path, body string) error {
	payload, _ := json.Marshal(map[string]any{
		"commit_id":    head,
		"path":         path,
		"subject_type": "file",
		"body":         body,
	})
	_, err := c.post(fmt.Sprintf("%s/repos/%s/pulls/%d/comments", apiBase, c.repo, pr), payload)
	return err
}

// deleteComment removes one review comment, and with it the thread it heads.
//
// GitHub offers no mutation for deleting a review THREAD, so the thread is
// taken away by deleting the comment it hangs off. That is only ever done to a
// conversation this program opened, identified by its own marker, carrying a
// digest the pull request no longer has, and that nobody resolved.
func (c *client) deleteComment(id int64) error {
	url := fmt.Sprintf("%s/repos/%s/pulls/comments/%d", apiBase, c.repo, id)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		// Not quoted: the body can echo the request, and this lands in a
		// public Actions log.
		return fmt.Errorf("DELETE review comment %d: HTTP %d", id, resp.StatusCode)
	}
	return nil
}

func (c *client) post(url string, payload []byte) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		// The body is not quoted: it can echo the request, and this output
		// lands in a public Actions log.
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return raw, nil
}

func split(repo string) (owner, name string, err error) {
	for i := 0; i < len(repo); i++ {
		if repo[i] == '/' {
			return repo[:i], repo[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("repo %q is not owner/name", repo)
}
