package cloudflare

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Errorf("the request did not carry the token")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestGetReturnsTheResultOfASuccess(t *testing.T) {
	base := serve(t, 200, `{"success":true,"result":[{"id":"a"}]}`)
	got, err := Get[[]struct {
		ID string `json:"id"`
	}](base, "fixture", "/x", nil)
	if err != nil || len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("got %v, %v", got, err)
	}
}

// Neither half of a refusal is trusted alone: a 200 whose envelope says it
// failed, and a failure status with a well-formed body, are both errors.
func TestARefusalIsAnErrorNamingItsReasonNeverAnEmptyResult(t *testing.T) {
	for _, c := range []struct {
		status int
		body   string
	}{
		{403, `{"success":false,"errors":[{"code":10000,"message":"Authentication error"}],"result":null}`},
		{200, `{"success":false,"errors":[{"code":10000,"message":"Authentication error"}],"result":[]}`},
	} {
		_, err := Get[[]string](serve(t, c.status, c.body), "fixture", "/x", nil)
		if err == nil || !strings.Contains(err.Error(), "Authentication error") {
			t.Errorf("%d %s: want an error naming the reason, got %v", c.status, c.body, err)
		}
	}
	if _, err := Get[[]string](serve(t, 502, `<html>bad gateway</html>`), "fixture", "/x", nil); err == nil {
		t.Error("an answer that is not the envelope was accepted")
	}
}

func TestR2EndpointIsTheAccountsOwnS3Host(t *testing.T) {
	if got := R2Endpoint("0123abcd"); got != "https://0123abcd.r2.cloudflarestorage.com" {
		t.Errorf("got %s", got)
	}
}

// rclone is handed one bucket's key through its environment and nowhere else,
// under the remote's own name, pointed at the account's own storage.
func TestRcloneIsConfiguredForOneRemoteThroughItsEnvironment(t *testing.T) {
	env := strings.Join(RcloneEnv("COPIES", "acct", "key-id", "key-secret"), "\n")
	for _, want := range []string{
		RcloneVar("COPIES", RcloneKeyID) + "=key-id",
		RcloneVar("COPIES", "SECRET_ACCESS_KEY") + "=key-secret",
		RcloneVar("COPIES", "ENDPOINT") + "=" + R2Endpoint("acct"),
		RcloneVar("COPIES", "TYPE") + "=s3",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("rclone is not given %s:\n%s", want, env)
		}
	}
	if strings.Contains(env, "OTHER") || !strings.HasSuffix(RcloneVar("OTHER", "TYPE"), "_OTHER_TYPE") {
		t.Errorf("a setting is not named for the remote it belongs to:\n%s", env)
	}
}
