package main

import "testing"

// The owner and repo come from whatever `git remote get-url origin` says, and
// that is not one format. A wrong parse here sends an authenticated API call
// at the wrong repository, so every form git actually emits is covered.
func TestParseRemote(t *testing.T) {
	cases := []struct {
		name, url, owner, repo string
		wantErr                bool
	}{
		{name: "https with .git", url: "https://github.com/fensberg/homelab.git", owner: "fensberg", repo: "homelab"},
		{name: "https without .git", url: "https://github.com/fensberg/homelab", owner: "fensberg", repo: "homelab"},
		{name: "trailing slash", url: "https://github.com/fensberg/homelab/", owner: "fensberg", repo: "homelab"},
		{name: "ssh scp-style", url: "git@github.com:fensberg/homelab.git", owner: "fensberg", repo: "homelab"},
		{name: "ssh url form", url: "ssh://git@github.com/fensberg/homelab.git", owner: "fensberg", repo: "homelab"},
		// A token embedded in the remote is a credential this program must not
		// carry into an API call or an error message.
		{name: "https with credentials", url: "https://x-access-token:secret@github.com/fensberg/homelab.git", owner: "fensberg", repo: "homelab"},
		{name: "a repo name containing a dot", url: "https://github.com/fensberg/home.lab.git", owner: "fensberg", repo: "home.lab"},

		{name: "not github", url: "https://gitlab.com/fensberg/homelab.git", wantErr: true},
		{name: "no repo", url: "https://github.com/fensberg", wantErr: true},
		{name: "empty", url: "", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner, repo, err := parseRemote(tc.url)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %s/%s", owner, repo)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if owner != tc.owner || repo != tc.repo {
				t.Errorf("got %s/%s, want %s/%s", owner, repo, tc.owner, tc.repo)
			}
		})
	}
}

// A parse failure must not echo the URL, because the URL is one of the places
// a credential legitimately lives.
func TestParseRemoteErrorDoesNotLeakCredentials(t *testing.T) {
	_, _, err := parseRemote("https://x-access-token:supersecretvalue@gitlab.com/a/b.git")
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := err.Error(); contains(got, "supersecretvalue") {
		t.Errorf("the error quotes the remote URL, credentials included: %s", got)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		}()
}
