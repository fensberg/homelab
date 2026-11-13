// Command pinnedactions writes what each action this repository pins runs in
// turn, at the commit it is pinned to, as JSON for the configuration rules to
// judge with no network (#881):
//
//	{"pinned_actions": {"owner/repo[/path]@sha": ["what it runs", ...], ...}}
//
// Every action a workflow or a composite action here names by owner and
// commit is fetched, and every action one of those runs, all the way down:
// GitHub follows them the same way when a job is set up, and refuses the job
// if any one is not allowed. Each is read from its metadata (action.yml or
// action.yaml) at its own commit, and nothing of it is run.
//
// What an action runs is written as it names it. One pinned by commit is
// fetched in turn; anything else - a tag, `./`, `docker://` - is kept as it is
// written, and the rule refuses what the suppliers list does not name.
//
//	pinnedactions <out.json>
//
// Run from the top of a checkout by .github/workflows/offline before a check,
// in the image the check runs in, with the network on: it lives beside that
// action, behind the same workflows permission, because what runs with a
// network is not the pull request's to change. YAML is read by Conftest's own
// parser, the one the rules read it with, which the image holds, so this
// needs nothing beyond the standard library.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// pinned is an action named by owner, repository, path in it, and commit.
var pinned = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(/[^@]+)?@[0-9a-f]{40}$`)

// sourceEnv points the fetch somewhere other than GitHub; only a test does.
const sourceEnv = "PINNED_ACTIONS_SOURCE"

const defaultSource = "https://raw.githubusercontent.com"

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: pinnedactions <out.json>")
		os.Exit(2)
	}
	source := os.Getenv(sourceEnv)
	if source == "" {
		source = defaultSource
	}
	n, err := write(os.Args[1], source, client())
	if err != nil {
		fmt.Fprintln(os.Stderr, "pinnedactions:", err)
		os.Exit(1)
	}
	fmt.Printf("pinnedactions: %d action(s) at their commits, written to %s\n", n, os.Args[1])
}

// client fetches over https, and reads file:// as a test's source.
func client() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.RegisterProtocol("file", http.NewFileTransport(http.Dir("/")))
	return &http.Client{Transport: transport, Timeout: 30 * time.Second}
}

// write fetches what every pinned action runs and writes it to out.
func write(out, source string, c *http.Client) (int, error) {
	files, err := tracked()
	if err != nil {
		return 0, err
	}
	named, err := uses(files...)
	if err != nil {
		return 0, err
	}
	graph, err := fetchAll(named, func(ref string) ([]byte, error) { return metadata(c, source, ref) })
	if err != nil {
		return 0, err
	}
	body, err := json.MarshalIndent(map[string]map[string][]string{"pinned_actions": graph}, "", "  ")
	if err != nil {
		return 0, err
	}
	return len(graph), os.WriteFile(out, append(body, '\n'), 0o644)
}

// tracked is every YAML file under .github the checkout tracks.
func tracked() ([]string, error) {
	out, err := exec.Command("git", "ls-files", "-z", "--", ".github/*.yml", ".github/*.yaml").Output()
	if err != nil {
		return nil, fmt.Errorf("listing the workflows and actions: %w", err)
	}
	var files []string
	for _, f := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// fetchAll follows every pinned action from the ones named here, all the way
// down, and says what each runs.
func fetchAll(named []string, fetch func(ref string) ([]byte, error)) (map[string][]string, error) {
	var queue []string
	for _, ref := range named {
		if pinned.MatchString(ref) {
			queue = append(queue, ref)
		}
	}
	if len(queue) == 0 {
		return nil, errors.New("no action here is pinned by commit, so there was nothing to fetch, which cannot be right")
	}
	graph := map[string][]string{}
	for len(queue) > 0 {
		ref := queue[0]
		queue = queue[1:]
		if _, seen := graph[ref]; seen {
			continue
		}
		body, err := fetch(ref)
		if err != nil {
			return nil, err
		}
		runs, err := usesIn(body)
		if err != nil {
			return nil, fmt.Errorf("reading what %s runs: %w", ref, err)
		}
		graph[ref] = runs
		for _, r := range runs {
			if pinned.MatchString(r) {
				queue = append(queue, r)
			}
		}
	}
	return graph, nil
}

// metadata is an action's action.yml, or action.yaml, at its commit.
func metadata(c *http.Client, source, ref string) ([]byte, error) {
	name, sha, _ := strings.Cut(ref, "@")
	parts := strings.SplitN(name, "/", 3)
	at := strings.TrimSuffix(source, "/") + "/" + path.Join(parts[0], parts[1], sha)
	if len(parts) == 3 {
		at += "/" + parts[2]
	}
	for _, file := range []string{"action.yml", "action.yaml"} {
		resp, err := c.Get(at + "/" + file)
		if err != nil {
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr == nil && resp.StatusCode == http.StatusOK {
			return body, nil
		}
	}
	return nil, fmt.Errorf("no action.yml or action.yaml at %s, so what it runs is unknown", ref)
}

// usesIn is what one action's metadata runs.
func usesIn(body []byte) ([]string, error) {
	f, err := os.CreateTemp("", "metadata-*.yml")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return uses(f.Name())
}

// uses is every `uses:` the files name in any step, wherever it is in them,
// read as YAML by Conftest's own parser: each once, in order.
func uses(files ...string) ([]string, error) {
	args := append([]string{"parse", "--combine", "--parser", "yaml"}, files...)
	out, err := exec.Command("conftest", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("reading %d file(s) as YAML with conftest: %w", len(files), err)
	}
	var docs any
	if err := json.Unmarshal(out, &docs); err != nil {
		return nil, fmt.Errorf("conftest's reading is not JSON: %w", err)
	}
	named := walk(docs, []string{})
	sort.Strings(named)
	return slices.Compact(named), nil
}

// walk appends to named every `uses:` under node.
func walk(node any, named []string) []string {
	switch v := node.(type) {
	case map[string]any:
		if ref, ok := v["uses"].(string); ok {
			named = append(named, ref)
		}
		for _, child := range v {
			named = walk(child, named)
		}
	case []any:
		for _, child := range v {
			named = walk(child, named)
		}
	}
	return named
}
