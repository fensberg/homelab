package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	outerSHA = "1111111111111111111111111111111111111111"
	innerSHA = "2222222222222222222222222222222222222222"
)

func TestMain(m *testing.M) {
	// A fixture repository's git must not read the machine's configuration.
	_ = os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	_ = os.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	os.Exit(m.Run())
}

// fixture is a repository naming actions, and a source holding their
// metadata at their commits, read over file:// the way GitHub's is over https.
func fixture(t *testing.T, workflow string) (repo, source string) {
	t.Helper()
	dir := t.TempDir()
	repo, source = filepath.Join(dir, "repo"), filepath.Join(dir, "source")
	write := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(repo, ".github", "workflows", "w.yml"), workflow)
	// The outer action runs another by its path in its repository, and an
	// image, and is read from action.yml.
	write(filepath.Join(source, "owner", "outer", outerSHA, "action.yml"),
		"runs:\n  using: composite\n  steps:\n"+
			"    - uses: owner/inner/sub@"+innerSHA+"\n"+
			"    - uses: docker://example.test/image:1\n"+
			"    - run: echo not an action\n      shell: bash\n")
	// The inner one is read from action.yaml, the other name GitHub takes.
	write(filepath.Join(source, "owner", "inner", innerSHA, "sub", "action.yaml"),
		"runs:\n  using: node24\n  main: index.js\n")
	for _, args := range [][]string{{"init", "-q", repo}, {"-C", repo, "add", "-A"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repo, "file://" + source
}

// run writes the fetch from the fixture repository, as the action runs it.
func run(t *testing.T, repo, source string) (map[string][]string, error) {
	t.Helper()
	t.Chdir(repo)
	out := filepath.Join(t.TempDir(), "pinned-actions.json")
	if _, err := write(out, source, client()); err != nil {
		return nil, err
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("it succeeded and wrote nothing: %v", err)
	}
	var got struct {
		PinnedActions map[string][]string `json:"pinned_actions"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("it wrote what is not JSON of the shape the rules read: %v\n%s", err, body)
	}
	return got.PinnedActions, nil
}

// Every action pinned here is fetched, and every action one of those runs,
// all the way down: by its path in its repository, from either name of its
// metadata, with what is not pinned by commit kept as written.
func TestWhatEachPinnedActionRunsIsFetchedAllTheWayDown(t *testing.T) {
	repo, source := fixture(t, "jobs:\n  a:\n    steps:\n"+
		"      - uses: owner/outer@"+outerSHA+"\n"+
		"      - uses: $/.github/workflows/local\n")
	got, err := run(t, repo, source)
	if err != nil {
		t.Fatalf("the fetch failed: %v", err)
	}
	want := map[string][]string{
		"owner/outer@" + outerSHA:     {"docker://example.test/image:1", "owner/inner/sub@" + innerSHA},
		"owner/inner/sub@" + innerSHA: {},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("what the pinned actions run was\n  %v\nwant\n  %v", got, want)
	}
}

// An action whose metadata is not where its pin says is refused by name,
// rather than written down as running nothing.
func TestAnActionThatCannotBeFetchedIsRefused(t *testing.T) {
	repo, source := fixture(t, "jobs:\n  a:\n    steps:\n      - uses: owner/gone@"+outerSHA+"\n")
	_, err := run(t, repo, source)
	if err == nil || !strings.Contains(err.Error(), "no action.yml or action.yaml at owner/gone@"+outerSHA) {
		t.Errorf("an action with no metadata was not refused by name: %v", err)
	}
}

// A repository that pins nothing by commit is refused: the rules would
// otherwise judge an empty fetch and pass.
func TestNothingPinnedIsRefused(t *testing.T) {
	repo, source := fixture(t, "jobs:\n  a:\n    steps:\n      - uses: owner/outer@v1\n")
	_, err := run(t, repo, source)
	if err == nil || !strings.Contains(err.Error(), "no action here is pinned by commit") {
		t.Errorf("a repository pinning nothing was not refused: %v", err)
	}
}

// Each action is fetched once, however many name it, and a cycle ends.
func TestAnActionNamedTwiceIsFetchedOnce(t *testing.T) {
	a, b := "o/a@"+outerSHA, "o/b@"+innerSHA
	calls := map[string]int{}
	graph, err := fetchAll([]string{a, a, "o/c@v1"}, func(ref string) ([]byte, error) {
		calls[ref]++
		if ref == a {
			return []byte("runs:\n  steps:\n    - uses: " + b + "\n"), nil
		}
		return []byte("runs:\n  steps:\n    - uses: " + a + "\n"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls[a] != 1 || calls[b] != 1 || len(graph) != 2 {
		t.Errorf("fetched %v into %v; want each of the two once", calls, graph)
	}
}

// A fetch that fails, or metadata that is not YAML, stops the whole fetch.
func TestAFailedFetchOrUnreadableMetadataStopsIt(t *testing.T) {
	ref := "o/a@" + outerSHA
	if _, err := fetchAll([]string{ref}, func(string) ([]byte, error) { return nil, errors.New("down") }); err == nil {
		t.Error("a failed fetch was taken as running nothing")
	}
	if _, err := fetchAll([]string{ref}, func(string) ([]byte, error) { return []byte("runs: [not closed"), nil }); err == nil || !strings.Contains(err.Error(), "reading what "+ref+" runs") {
		t.Errorf("metadata that is not YAML was taken as running nothing: %v", err)
	}
}

// A source that does not answer is refused by name, the same as one that
// answers without the action: neither is an action that runs nothing.
func TestASourceThatDoesNotAnswerIsRefused(t *testing.T) {
	ref := "owner/outer@" + outerSHA
	_, err := metadata(client(), "unknown-scheme://nowhere", ref)
	if err == nil || !strings.Contains(err.Error(), "no action.yml or action.yaml at "+ref) {
		t.Errorf("a source that did not answer was not refused by name: %v", err)
	}
}
