package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"homelab/details/workorders"
)

// judge decides whether a delivery brings production anything but a new Steam
// build. It reads what the registry recorded about the two releases - the
// commit each was built from - and compares those commits over the inputs
// the work order says the release is made of. Nothing in the pull request
// itself is trusted for this: a delivery's body or branch name could say
// anything.
type judge struct {
	repository string // owner/name
	releases   string // clusters/management/releases.yaml
	orders     string // scripts/work-orders.json
	pin        string // scripts/versions.env
	git        func(args ...string) (string, error)
}

func gitRunner(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	return string(out), err
}

// registryBase is a variable so tests can serve the registry locally.
var registryBase = "https://ghcr.io"

func (j judge) steamOnly(base, head string) ([]string, error) {
	before, err := j.pinsAt(base)
	if err != nil {
		return nil, err
	}
	after, err := j.pinsAt(head)
	if err != nil {
		return nil, err
	}
	orders, err := j.ordersAt(head)
	if err != nil {
		return nil, err
	}

	var problems []string
	for _, name := range sortedNames(after) {
		now, was := after[name], before[name]
		if now.Digest == was.Digest {
			continue
		}
		if was.Digest == "" {
			problems = append(problems, name+" has no release in production yet, so there is nothing to compare its first one with")
			continue
		}
		order, ok := orders[name]
		if !ok || order.Release == nil {
			problems = append(problems, name+" has no work order that makes a release")
			continue
		}
		pkg := strings.ToLower(j.repository) + "-" + name + "-release"
		from, err := releaseRevision(pkg, was.Digest)
		if err != nil {
			return nil, fmt.Errorf("%s's release in production: %w", name, err)
		}
		to, err := releaseRevision(pkg, now.Digest)
		if err != nil {
			return nil, fmt.Errorf("%s's delivered release: %w", name, err)
		}
		if _, err := j.git("fetch", "--no-tags", "--depth=1", "origin", from, to); err != nil {
			return nil, fmt.Errorf("fetching the commits %s's releases were built from: %w", name, err)
		}
		found, err := j.compare(from, to, order)
		if err != nil {
			return nil, err
		}
		for _, p := range found {
			problems = append(problems, name+": "+p)
		}
	}
	return problems, nil
}

// compare is the rule itself, over two source commits. The image's inputs -
// its build context and the pins file - may differ by the Steam build line
// alone. The manifests it ships in - the overlay and the module - may differ
// by comments alone, because a comment reaches no cluster. The work orders
// may not differ at all.
func (j judge) compare(from, to string, o workorders.Order) ([]string, error) {
	var problems []string

	files, lines, err := j.diff(from, to, o.Context, j.pin)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if f != j.pin {
			problems = append(problems, "the image's build context changed: "+f)
		}
	}
	if len(files) == 1 && files[0] == j.pin {
		problems = append(problems, onlyTheSteamBuild(lines)...)
	}
	if len(files) == 0 {
		problems = append(problems, "the image's inputs did not change, so this is not a new Steam build")
	}

	_, lines, err = j.diff(from, to, o.Release.Overlay, o.Release.Module)
	if err != nil {
		return nil, err
	}
	for _, l := range lines {
		if !commentOrBlank.MatchString(l) {
			problems = append(problems, "the manifests it ships changed: "+strings.TrimSpace(l))
		}
	}

	files, _, err = j.diff(from, to, j.orders)
	if err != nil {
		return nil, err
	}
	if len(files) > 0 {
		problems = append(problems, "the work orders changed")
	}
	return problems, nil
}

// commentOrBlank is a changed YAML line that reaches no cluster.
var commentOrBlank = regexp.MustCompile(`^[+-]\s*(#.*)?$`)

func (j judge) diff(from, to string, paths ...string) ([]string, []string, error) {
	args := append([]string{"diff", "--name-only", from, to, "--"}, paths...)
	names, err := j.git(args...)
	if err != nil {
		return nil, nil, fmt.Errorf("git diff: %w", err)
	}
	args = append([]string{"diff", "--unified=0", from, to, "--"}, paths...)
	body, err := j.git(args...)
	if err != nil {
		return nil, nil, fmt.Errorf("git diff: %w", err)
	}
	return nonEmptyLines(names), changedLines(body), nil
}

type pin struct{ Tag, Digest string }

// pinsAt reads each release's pin from the releases file at a commit.
func (j judge) pinsAt(commit string) (map[string]pin, error) {
	body, err := j.git("show", commit+":"+j.releases)
	if err != nil {
		return nil, fmt.Errorf("reading %s at %s: %w", j.releases, commit, err)
	}
	return releasePins(body)
}

// releasePins reads the OCIRepository documents of the releases file: each
// one's name, and the tag and digest it pins. Read line by line rather than
// through a YAML library, because this module takes no dependencies; the
// file's shape is this repository's own, and a document it cannot read
// pins nothing, which fails the judgement rather than passing it.
func releasePins(body string) (map[string]pin, error) {
	out := map[string]pin{}
	for _, doc := range strings.Split("\n"+body, "\n---") {
		var kind, name string
		var p pin
		inMetadata := false
		for _, line := range strings.Split(doc, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			indented := line[0] == ' '
			if !indented {
				inMetadata = trimmed == "metadata:"
			}
			key, value, ok := strings.Cut(trimmed, ":")
			if !ok {
				continue
			}
			value = strings.Trim(strings.TrimSpace(value), `"'`)
			switch {
			case !indented && key == "kind":
				kind = value
			case inMetadata && key == "name" && name == "":
				name = value
			case key == "tag":
				p.Tag = value
			case key == "digest":
				p.Digest = value
			}
		}
		if kind == "OCIRepository" && name != "" {
			out[name] = p
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the releases file pins no release")
	}
	return out, nil
}

func (j judge) ordersAt(commit string) (map[string]workorders.Order, error) {
	body, err := j.git("show", commit+":"+j.orders)
	if err != nil {
		return nil, fmt.Errorf("reading %s at %s: %w", j.orders, commit, err)
	}
	orders, err := workorders.Parse([]byte(body))
	if err != nil {
		return nil, err
	}
	out := map[string]workorders.Order{}
	for _, o := range orders {
		out[o.Name] = o
	}
	return out, nil
}

// releaseRevision asks the registry which commit a release was built from.
// The fabricator publishes each with `flux push artifact --revision
// <tag>@sha1:<commit>`, which records it as the manifest's
// org.opencontainers.image.revision annotation. The packages are public, so
// an anonymous pull token is enough.
func releaseRevision(pkg, digest string) (string, error) {
	client := &http.Client{Timeout: 20 * time.Second}

	resp, err := client.Get(registryBase + "/token?scope=repository:" + pkg + ":pull")
	if err != nil {
		return "", fmt.Errorf("asking the registry for a pull token: %w", err)
	}
	var tok struct {
		Token string `json:"token"`
	}
	err = json.NewDecoder(resp.Body).Decode(&tok)
	resp.Body.Close()
	if err != nil || tok.Token == "" {
		return "", fmt.Errorf("the registry gave no pull token for %s (HTTP %d)", pkg, resp.StatusCode)
	}

	req, err := http.NewRequest(http.MethodGet, registryBase+"/v2/"+pkg+"/manifests/"+digest, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json")
	resp, err = client.Do(req)
	if err != nil {
		return "", fmt.Errorf("reading %s@%s: %w", pkg, digest, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the registry answered %d for %s@%s", resp.StatusCode, pkg, digest)
	}
	var m struct {
		Annotations map[string]string `json:"annotations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return "", fmt.Errorf("reading %s@%s: %w", pkg, digest, err)
	}
	rev := m.Annotations["org.opencontainers.image.revision"]
	_, sha, ok := strings.Cut(rev, "@sha1:")
	if !ok || !fullSHA.MatchString(sha) {
		return "", fmt.Errorf("%s@%s records no source commit (revision %q)", pkg, digest, rev)
	}
	return sha, nil
}

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

func sortedNames(m map[string]pin) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
