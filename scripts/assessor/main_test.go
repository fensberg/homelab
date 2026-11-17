package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homelab/details/ghapi"
	"homelab/details/gitenv"
)

func TestMain(m *testing.M) {
	gitenv.Isolate()
	os.Exit(m.Run())
}

// at is a moment in a made-up history.
func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// fixture is a git repository with a made-up history: one commit per entry,
// each dated as given and writing the files given.
func fixture(t *testing.T, commits []struct {
	When  string
	Files map[string]string
}) string {
	t.Helper()
	dir := t.TempDir()
	git := func(env []string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		cmd.Env = append(os.Environ(), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(nil, "init", "-q", "-b", "main")
	for _, c := range commits {
		for name, body := range c.Files {
			path := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		git(nil, "add", "-A")
		git([]string{"GIT_AUTHOR_DATE=" + c.When, "GIT_COMMITTER_DATE=" + c.When}, "commit", "-q", "-m", "a change")
	}
	return dir
}

// history of a small repository: a guard answering issue #12, a rule
// answering #13, a required set, and two edits to the guidance.
func smallHistory(t *testing.T) string {
	return fixture(t, []struct {
		When  string
		Files map[string]string
	}{
		{"2026-01-01T00:00:00Z", map[string]string{
			"GUIDE.md": "one\n",
			"guards/a_test.go": "package guards\n\n// Nothing is built twice (#12).\nfunc TestNothingTwice(t *testing.T) {}\n\n" +
				"// No issue cited here.\nfunc TestUncited(t *testing.T) {}\n",
			"rules/kept/kept.rego":      "# Everything kept says for how long (#13, #99).\npackage rules.kept\n",
			"rules/kept/kept_test.rego": "# A test of the rule (#14).\npackage rules.kept\n",
		}},
		{"2026-01-10T00:00:00Z", map[string]string{
			"GUIDE.md":       "two\n",
			"infra/rules.tf": "locals {\n  required_checks = [\n    # the gate\n    \"tests passed\",\n    \"Gate\",\n  ]\n}\n",
		}},
		{"2026-01-20T00:00:00Z", map[string]string{"GUIDE.md": "three\n"}},
	})
}

// fakeGitHub answers the questions the report asks, from a table of paths.
func fakeGitHub(t *testing.T, answers map[string]string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// A filtered listing of runs stops at a thousand without saying so,
		// so the report must never ask for one.
		if r.URL.Query().Get("status") != "" || r.URL.Query().Get("event") != "" {
			t.Errorf("asked for a filtered listing: %s", r.URL)
		}
		body, ok := answers[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// The whole program, against a made-up repository and a stand-in GitHub:
// one pull request whose first push failed a guard that answers an issue,
// and the report says so in each section.
func TestTheReportIsWrittenFromTheAPIAndTheHistory(t *testing.T) {
	dir := smallHistory(t)
	const repo = "/repos/o/r"
	api := fakeGitHub(t, map[string]string{
		repo + "/pulls": `[
			{"number":1,"created_at":"2026-01-05T00:00:00Z","closed_at":"2026-01-06T00:00:00Z","user":{"login":"agent"},"head":{"ref":"piece-one"}},
			{"number":2,"created_at":"2026-01-25T00:00:00Z","user":{"login":"agent"},"head":{"ref":"piece-two"}},
			{"number":3,"created_at":"2026-01-25T00:00:00Z","user":{"login":"someone"},"head":{"ref":"theirs"}}]`,
		repo + ghapi.WorkflowRuns: `{"workflow_runs":[
			{"id":5,"event":"pull_request","head_branch":"piece-one","head_sha":"aaa","created_at":"2026-01-05T00:01:00Z","conclusion":"failure"},
			{"id":6,"event":"pull_request","head_branch":"piece-two","head_sha":"bbb","created_at":"2026-01-25T00:01:00Z","conclusion":"success"}]}`,
		repo + "/commits/aaa/check-runs": `{"check_runs":[
			{"name":"tests passed","conclusion":"failure","id":1},
			{"name":"Gate","conclusion":"failure","id":1}]}`,
		repo + "/commits/bbb/check-runs": `{"check_runs":[
			{"name":"tests passed","conclusion":"success","id":2},
			{"name":"Gate","conclusion":"failure","id":2}]}`,
		repo + "/actions/runs/5/jobs": `{"jobs":[
			{"id":9,"name":"test / repo policy","conclusion":"failure","steps":[{"name":"Run","conclusion":"failure"}]},
			{"id":10,"name":"tests passed","conclusion":"failure","steps":[{"name":"Every lane passed","conclusion":"failure"}]}]}`,
		repo + "/actions/jobs/9/logs":  "2026-01-05T00:03:00.0000000Z --- FAIL: TestNothingTwice (0.00s)\n",
		repo + "/actions/jobs/10/logs": "",
		repo + "/issues/12":            `{"created_at":"2025-12-20T00:00:00Z"}`,
		repo + "/issues/13":            `{"created_at":"2025-12-21T00:00:00Z","pull_request":{}}`,
		repo + "/pulls/1/files":        `[{"filename":"README.md"}]`,
	})
	out := filepath.Join(t.TempDir(), "report.md")
	env := map[string]string{"GH_TOKEN": "tok", "GITHUB_REPOSITORY": "o/r"}
	code := run([]string{"-agent", "agent", "-rev", "HEAD", "-dir", dir, "-api", api,
		"-cache", filepath.Join(t.TempDir(), "cache"), "-out", out, "-gate", "Gate",
		"-guards", "guards", "-rules", "rules", "-guidance", "GUIDE.md", "-window", "1"},
		func(k string) string { return env[k] })
	if code != 0 {
		t.Fatalf("run returned %d", code)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	report := string(body)
	for _, want := range []string{
		"| Opened by the AI developer's account                | 2     |",
		"| 2026-01 | 0 of 2 (0.0%) | 1 of 2 (50.0%)",
		"| Repository guards (Go, Conftest and Semgrep rules of the estate) | 1           | 100.0% |",
		"`TestNothingTwice`",
		"1 of 1 (100.0%)",
		"100.0% to 100.0% (1 to 1 of 1)",
		"| Median, days              | 12.0  |",
		"3 commits changed the file",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not say %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "someone") || strings.Contains(report, "theirs") {
		t.Error("another account's pull request reached the report")
	}
}

// Without what it needs, the program says so and writes nothing.
func TestTheProgramRefusesToRunWithoutWhatItNeeds(t *testing.T) {
	out := filepath.Join(t.TempDir(), "report.md")
	none := func(string) string { return "" }
	where := []string{"-gate", "g", "-guards", "guards", "-rules", "rules", "-guidance", "GUIDE.md"}
	for name, c := range map[string]struct {
		args []string
		env  func(string) string
		want int
	}{
		"no repository":   {append([]string{"-agent", "a", "-cache", "c", "-out", out}, where...), none, 2},
		"no guards named": {[]string{"-repo", "o/r", "-agent", "a", "-cache", "c", "-out", out}, func(string) string { return "tok" }, 2},
		"no token":        {append([]string{"-repo", "o/r", "-agent", "a", "-cache", t.TempDir(), "-out", out}, where...), none, 2},
		"an unknown flag": {[]string{"-nonsense"}, none, 2},
		"a history that is not one": {append([]string{"-repo", "o/r", "-agent", "a", "-cache", t.TempDir(), "-out", out,
			"-dir", t.TempDir()}, where...), func(string) string { return "tok" }, 1},
	} {
		if got := run(c.args, c.env); got != c.want {
			t.Errorf("%s: run returned %d, want %d", name, got, c.want)
		}
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a refused run wrote a report")
	}
}
