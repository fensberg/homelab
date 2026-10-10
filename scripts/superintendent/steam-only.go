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

	"homelab/details/applications"
	"homelab/details/drafter"
	"homelab/details/flux"
	"homelab/details/gitenv"
)

// judge decides whether a delivery brings a site anything but a new build
// from the application's supplier. It reads what the registry recorded about
// the two releases - the commit each was built from - and compares those
// commits over what the release is made of: the application's own directory,
// and the estate's pins and orders. Nothing in the pull request itself is
// trusted for this: a delivery's body or branch name could say anything.
type judge struct {
	repository string // owner/name
	orders     string // the estate's own work orders
	pins       string // the estate's own pins
	git        func(args ...string) (string, error)
}

func gitRunner(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	return string(out), err
}

// registryBase is a variable so tests can serve the registry locally.
var registryBase = "https://ghcr.io"

// upstreamOnly lists everything a delivery to one site's file brings beyond
// the supplier's build. site is that file.
func (j judge) upstreamOnly(site, base, head string) ([]string, error) {
	before, err := j.pinsAt(site, base)
	if err != nil {
		return nil, err
	}
	after, err := j.pinsAt(site, head)
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
			problems = append(problems, name+" has no release in "+site+" yet, so there is nothing to compare its first one with")
			continue
		}
		pkg := strings.ToLower(j.repository) + "-" + name + "-release"
		from, err := releaseRevision(pkg, was.Digest)
		if err != nil {
			return nil, fmt.Errorf("%s's release in %s: %w", name, site, err)
		}
		to, err := releaseRevision(pkg, now.Digest)
		if err != nil {
			return nil, fmt.Errorf("%s's delivered release: %w", name, err)
		}
		if _, err := j.git("fetch", "--no-tags", "--depth=1", "origin", from, to); err != nil {
			return nil, fmt.Errorf("fetching the commits %s's releases were built from: %w", name, err)
		}
		// What the application declared when the running release was built,
		// which is the declaration nothing in the new release could have
		// written.
		declared, err := j.declaredAt(from)
		if err != nil {
			return nil, err
		}
		app, ok := declared[name]
		switch {
		case !ok:
			problems = append(problems, name+" is not an application the release in "+site+" was built with")
			continue
		case app.Release == nil:
			problems = append(problems, name+" does not declare how a release of it is made")
			continue
		case app.Upstream == nil:
			problems = append(problems, name+" declares no upstream, so there is no supplier's build its release could differ by")
			continue
		}
		found, err := j.compare(from, to, app)
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
// its build context, the application's pins and the estate's - may differ by
// the application's declared pin alone. Everything else in the application's
// directory ships in the release, and may differ by comments alone, because a
// comment reaches no cluster. Its declaration and the estate's work orders
// may not differ at all.
func (j judge) compare(from, to string, a applications.Application) ([]string, error) {
	var problems []string

	files, lines, err := j.diff(from, to, a.Image(), a.PinsFile(), j.pins)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if f != a.PinsFile() {
			problems = append(problems, "the image's inputs changed: "+f)
		}
	}
	if len(files) == 1 && files[0] == a.PinsFile() {
		problems = append(problems, onlyThePin(lines, a.Upstream.Pin)...)
	}
	if len(files) == 0 {
		problems = append(problems, "the image's inputs did not change, so this is not a new build from its supplier")
	}

	files, lines, err = j.diff(from, to, a.Root, exclude+a.Image(), exclude+a.PinsFile(), exclude+a.Path)
	if err != nil {
		return nil, err
	}
	shipped, err := j.shippedChanges(from, to, files, lines)
	if err != nil {
		return nil, err
	}
	problems = append(problems, shipped...)

	files, _, err = j.diff(from, to, a.Path, j.orders)
	if err != nil {
		return nil, err
	}
	if len(files) > 0 {
		problems = append(problems, "what says how it is built and released changed: "+strings.Join(files, ", "))
	}
	return problems, nil
}

// exclude marks a path the diff leaves out.
const exclude = "!"

// commentOrBlank is a changed line that is a comment, or nothing.
var commentOrBlank = regexp.MustCompile(`^[+-]\s*(#.*)?$`)

// shippedChanges is every way what an application ships differs between two
// commits, beyond its comments.
//
// A manifest is compared as what it is: redrawn at each commit, and the two
// drawings held against each other, so that what differs is what a cluster
// would be handed and a comment, a blank line or a key written another way
// is not. It used to be judged a line at a time, by whether each changed
// line looked like a comment. Anything that is not a manifest is still
// judged that way: a script's comment is a line beginning with one, and
// there is nothing more to it to read.
func (j judge) shippedChanges(from, to string, files, lines []string) ([]string, error) {
	var problems []string
	others := false
	for _, f := range files {
		if !strings.HasSuffix(f, ".yaml") && !strings.HasSuffix(f, ".yml") {
			others = true
			continue
		}
		was, err := j.drawnAt(from, f)
		if err != nil {
			return nil, err
		}
		is, err := j.drawnAt(to, f)
		if err != nil {
			return nil, err
		}
		if was != is {
			problems = append(problems, "the manifests it ships changed: "+f)
		}
	}
	if !others {
		return problems, nil
	}
	// With something that is not a manifest among the changes, every
	// changed line is held to the old rule. A manifest's lines are among
	// them, which only makes this stricter.
	for _, l := range lines {
		if !commentOrBlank.MatchString(l) {
			problems = append(problems, "what it ships changed: "+strings.TrimSpace(l))
		}
	}
	return problems, nil
}

// drawnAt is a manifest as it was at a commit, redrawn, as text two
// drawings can be compared by. One that was not there is an empty drawing,
// so that a manifest added or removed differs from itself.
func (j judge) drawnAt(commit, file string) (string, error) {
	body, err := j.git("show", commit+":"+file)
	if err != nil {
		return "", nil
	}
	drawn, err := drafter.Text(".yaml", []byte(body))
	if err != nil {
		return "", fmt.Errorf("%s at %s: %w", file, commit, err)
	}
	return string(drawn), nil
}

func (j judge) diff(from, to string, paths ...string) ([]string, []string, error) {
	// Anchored at the top of the repository: the verb runs from
	// scripts/superintendent, where a bare pathspec would name nothing.
	top := make([]string, len(paths))
	for i, p := range paths {
		if rest, excluded := strings.CutPrefix(p, exclude); excluded {
			top[i] = ":(top,exclude)" + rest
		} else {
			top[i] = ":(top)" + p
		}
	}
	paths = top
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

// pinsAt reads each release's pin from a site's file at a commit.
func (j judge) pinsAt(site, commit string) (map[string]pin, error) {
	body, err := j.git("show", commit+":"+site)
	if err != nil {
		return nil, fmt.Errorf("reading %s at %s: %w", site, commit, err)
	}
	return releasePins(body)
}

// releasePins reads the OCIRepository documents of a site's file: each
// one's name, and the tag and digest it pins.
//
// This took any line saying tag or digest, anywhere in a document, to be
// the pin, because this module takes no dependencies. It reads the document
// now, as the drafter redraws it, and a pin is what a release source says
// under spec.ref and nowhere else. A file that cannot be read pins nothing,
// which fails the judgement and never passes it.
func releasePins(body string) (map[string]pin, error) {
	docs, err := drafter.Documents([]byte(body))
	if err != nil {
		return nil, err
	}
	out := map[string]pin{}
	for _, doc := range docs {
		meta, _ := doc["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		if doc["kind"] != flux.OCIRepository || name == "" {
			continue
		}
		spec, _ := doc["spec"].(map[string]any)
		ref, _ := spec["ref"].(map[string]any)
		var p pin
		p.Tag, _ = ref["tag"].(string)
		p.Digest, _ = ref["digest"].(string)
		out[name] = p
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the site's file pins no release")
	}
	return out, nil
}

// declaredAt reads every application's declaration as it stood at a commit,
// by name. The same reading every other program makes of the working tree
// (homelab/details/applications), made of a commit instead: a declaration
// that does not parse there is an error, never an application with nothing
// to say.
func (j judge) declaredAt(commit string) (map[string]applications.Application, error) {
	// ls-tree of a directory that is not there lists nothing and succeeds,
	// which is a repository with no applications.
	listed, err := j.git("ls-tree", "-d", "--name-only", "--full-tree", commit, applications.Dir+"/")
	if err != nil {
		return nil, fmt.Errorf("listing the applications at %s: %w", commit, err)
	}
	out := map[string]applications.Application{}
	for _, dir := range nonEmptyLines(listed) {
		name := dir[strings.LastIndex(dir, "/")+1:]
		rel := applications.Dir + "/" + name + "/" + applications.Declaration
		body, err := j.git("show", commit+":"+rel)
		if err != nil {
			return nil, fmt.Errorf("reading %s at %s: %w", rel, commit, err)
		}
		a, err := applications.Parse(name, []byte(body))
		if err != nil {
			return nil, err
		}
		out[name] = a
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
	if !ok || !gitenv.IsCommit(sha) {
		return "", fmt.Errorf("%s@%s records no source commit (revision %q)", pkg, digest, rev)
	}
	return sha, nil
}

func sortedNames(m map[string]pin) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
