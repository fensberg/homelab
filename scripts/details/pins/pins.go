// Package pins is which commit of the estate's modules each site runs, and
// what would be different about a site if that commit moved.
//
// A site's roots build nothing themselves; they call modules
// (modules/infrastructure/), and each site runs them as they were at a commit
// named in management/pins.json - the estate's default, or the site's own.
// The contractor hands a site that commit's whole tree (homelab/contractor/pin),
// because a module reads more than itself: what applications declare, the
// manifests Flux is bootstrapped from, the CNI's.
//
// So "a change to the modules has merged, and the pin should move" is a
// question about more than one directory, and it is answered here by reading
// what the modules reach for rather than from a list somebody keeps: Reads
// is every path a module names outside itself, and Changed is whether any of
// them, or the modules, differ between two commits. Procurement asks it after
// every merge and brings the pin to the gate when the answer is yes.
package pins

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"homelab/details/gitenv"
	"homelab/details/tofufiles"
)

// File is where the pins are, from the top of the repository.
//
// Under management/, beside the roots, because that is what a converge
// applies: a merge that changes this file is planned on its pull request,
// converged when it lands, and returned with the rest of management/ if that
// converge fails. Anywhere else, moving a pin would be a change the estate's
// own machinery did not see.
const File = "management/pins.json"

// ModulesDir is where the modules a site's roots call are, from the top of
// the repository.
const ModulesDir = "modules/infrastructure"

// Pins is the estate's default and each site's own.
type Pins struct {
	// Default is the commit a site runs unless it names another. A site
	// nobody has pinned runs this, so bringing one online needs no pin.
	Default string `json:"default"`
	// Sites holds a commit for each site held at a version of its own,
	// ahead of the default or behind it.
	Sites map[string]string `json:"per_site"`
}

// Read loads the repository's pins.
func Read(repoRoot string) (Pins, error) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(File)))
	if err != nil {
		return Pins{}, fmt.Errorf("reading the pins: %w", err)
	}
	return Parse(raw)
}

// Parse reads the pins and refuses any that is not a full commit hash: a
// branch or a tag names whatever it points at today, which is not a pin.
func Parse(raw []byte) (Pins, error) {
	var p Pins
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, fmt.Errorf("%s is not the pins this reads (a default and a per_site map): %w", File, err)
	}
	if !gitenv.IsCommit(p.Default) {
		return p, fmt.Errorf("%s: the default pin %q is not a full commit hash. A site with no pin of its own runs the default, so there has to be one", File, p.Default)
	}
	for site, sha := range p.Sites {
		if !gitenv.IsCommit(sha) {
			return p, fmt.Errorf("%s: the pin for %s, %q, is not a full commit hash", File, site, sha)
		}
	}
	return p, nil
}

// For is the commit a site runs: its own pin, or the estate's default.
func (p Pins) For(site string) string {
	if sha, ok := p.Sites[site]; ok {
		return sha
	}
	return p.Default
}

// reach is a string in a module's code that leaves the module's directory:
// "${path.module}/../../../<somewhere>". The climb, and what follows it.
var reach = regexp.MustCompile(`\$\{path\.module\}((?:/\.\.)+)/([^"\s]*)`)

// Reads is what a pinned tree is read for: the modules, and every path a
// module names outside its own directory, from the top of the repository.
// A path is a file, a directory - everything beneath it - or a pattern in
// which * stands for one directory or file name.
//
// Read off the modules' own code, from the OpenTofu files given
// (tofufiles.Read), so a module that starts reading something new is seen
// without anybody adding it to a list. A reach this cannot follow - one
// built from a value, so that where it leads is not written down - is an
// error rather than a path passed over: a change to whatever it names would
// reach no site and nothing would say so.
func Reads(files map[string]string) ([]string, error) {
	seen := map[string]bool{ModulesDir: true}
	for rel, body := range files {
		if !within(ModulesDir, rel) || !strings.HasSuffix(rel, ".tf") {
			continue
		}
		for _, m := range reach.FindAllStringSubmatch(tofufiles.Code(body), -1) {
			if strings.Contains(m[2], "${") {
				return nil, fmt.Errorf("%s reaches outside its module through %q, and where that leads is not written in the path. Write the path whole, from the repository's top, so that a change to what it names is known to change what a site runs", rel, m[0])
			}
			to := path.Join(path.Dir(rel), strings.TrimPrefix(m[1], "/"), m[2])
			if strings.HasPrefix(to, "../") || to == ".." {
				return nil, fmt.Errorf("%s reaches %q, which is outside the repository", rel, m[0])
			}
			if !within(ModulesDir, to) {
				seen[to] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// within reports whether p is the path dir names or beneath it.
func within(dir, p string) bool { return p == dir || strings.HasPrefix(p, dir+"/") }

// matches reports whether a file is one a read names.
func matches(read, file string) bool {
	if strings.ContainsAny(read, "*?[") {
		ok, err := path.Match(read, file)
		return err == nil && ok
	}
	return within(read, file)
}

// Git runs git in the repository and returns its output. A parameter so a
// test can say what the repository holds.
type Git func(args ...string) ([]byte, error)

// Changed reports whether anything a pinned tree is read for differs between
// two commits, and names the first few files that do.
func Changed(git Git, from, to string, reads []string) ([]string, error) {
	before, err := readFiles(git, from, reads)
	if err != nil {
		return nil, err
	}
	after, err := readFiles(git, to, reads)
	if err != nil {
		return nil, err
	}
	var differ []string
	for file, hash := range after {
		if before[file] != hash {
			differ = append(differ, file)
		}
	}
	for file := range before {
		if _, still := after[file]; !still {
			differ = append(differ, file)
		}
	}
	sort.Strings(differ)
	return differ, nil
}

// readFiles is every file at a commit that a pinned tree is read for, with
// the hash of its contents.
func readFiles(git Git, commit string, reads []string) (map[string]string, error) {
	out, err := git("ls-tree", "-r", "--full-tree", commit)
	if err != nil {
		return nil, fmt.Errorf("listing the files at %s: %w", commit, err)
	}
	files := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		// <mode> <type> <hash>\t<path>
		meta, file, ok := strings.Cut(line, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			continue
		}
		for _, read := range reads {
			if matches(read, file) {
				files[file] = fields[2]
				break
			}
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s holds nothing a site reads, which is not this repository at any commit", commit)
	}
	return files, nil
}

// defaultLine is the one line of the pins file that names the default.
var defaultLine = regexp.MustCompile(`(?m)^(\s*"default":\s*")[0-9a-f]{40}(",?\s*)$`)

// MoveDefault is the pins file's text with the default moved to a commit, and
// nothing else changed: one line, so the pull request a person reviews is
// the move and nothing beside it.
func MoveDefault(body []byte, sha string) ([]byte, error) {
	if !gitenv.IsCommit(sha) {
		return nil, fmt.Errorf("%q is not a full commit hash, and a pin is nothing else", sha)
	}
	if n := len(defaultLine.FindAll(body, -1)); n != 1 {
		return nil, fmt.Errorf("%s names its default on %d lines this can read, so there is no telling which to move", File, n)
	}
	moved := defaultLine.ReplaceAll(body, []byte("${1}"+sha+"${2}"))
	if _, err := Parse(moved); err != nil {
		return nil, err
	}
	return moved, nil
}
