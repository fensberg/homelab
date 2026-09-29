package ghapi

import "testing"

func TestURLDefaultsToGitHubAndHonoursAnOverride(t *testing.T) {
	if got := URL("", "/repos/%s/actions/runs/%d", "owner/repository", 7); got != "https://api.github.com/repos/owner/repository/actions/runs/7" {
		t.Errorf("got %q", got)
	}
	if got := URL("http://127.0.0.1:9", "/x"); got != "http://127.0.0.1:9/x" {
		t.Errorf("an override was not used: %q", got)
	}
}
