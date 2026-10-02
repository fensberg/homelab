// Package platform is the estate's platform as it is released: what a
// release holds, and what a version of it is called.
//
// A site runs the estate's modules (modules/infrastructure/) and nothing
// built for it alone. So that merging a change does nothing to production,
// the modules are not consumed from the repository: every merge that changes
// them publishes a release - one immutable package in the registry, under a
// version - and a site runs the version its own config pins. Releasing to
// production is changing that line.
//
// A release is more than the modules' own directory, because a module reads
// more than itself: what applications declare, the manifests Flux is started
// from, the CNI's. Those go in the package at the paths they have in the
// repository, so a module finds them inside the package exactly as it finds
// them in a checkout.
//
// What a release holds is written in one file, the manifest, because the
// fabricator's packing step is shell and has to be told. That list cannot go
// stale: Reads is what the modules actually reach for, read off their code,
// and the tests hold the manifest to it in both directions - and planning
// both roots from a tree holding the package alone
// (scripts/contractor/internal/phases) proves it is enough.
package platform

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"homelab/details/tofufiles"
)

// ModulesDir is where the modules a site's roots call are, from the top of
// the repository.
const ModulesDir = "modules/infrastructure"

// Manifest is the file that says what a release of the platform holds, from
// the top of the repository.
const Manifest = ModulesDir + "/release.json"

// Version is what a release is called: the year and month it was published
// in, and its number within that month - v2026.10.1, then v2026.10.2. No
// leading zero on the month, so that the name is also a semantic version and
// every tool that orders versions orders these.
var Version = regexp.MustCompile(`^v(20[0-9]{2})\.([1-9]|1[0-2])\.([1-9][0-9]*)$`)

// Release is the manifest.
type Release struct {
	// Comment is for the reader of the file. Nothing reads it.
	Comment []string `json:"_comment,omitempty"`
	// Holds is every path the package carries, from the top of the
	// repository: a file, a directory - everything beneath it - or a
	// pattern in which * stands for one directory or file name.
	Holds []string `json:"holds"`
}

// Read loads the manifest and refuses one that names nothing.
func Read(repoRoot string) (Release, error) {
	var r Release
	raw, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(Manifest)))
	if err != nil {
		return r, fmt.Errorf("reading what a platform release holds: %w", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return r, fmt.Errorf("%s is not a manifest this reads: %w", Manifest, err)
	}
	if len(r.Holds) == 0 {
		return r, fmt.Errorf("%s holds nothing, so a release of it would be an empty package", Manifest)
	}
	return r, nil
}

// reach is a string in a module's code that leaves the module's directory:
// "${path.module}/../../../<somewhere>". The climb, and what follows it.
var reach = regexp.MustCompile(`\$\{path\.module\}((?:/\.\.)+)/?([^"\s]*)`)

// Reads is what the modules are read with: the modules, and every path a
// module names outside its own directory, from the top of the repository,
// in the form a manifest's holds takes.
//
// Read off the modules' own code, from the OpenTofu files given
// (tofufiles.Read), so a module that starts reading something new is seen
// without anybody remembering to say so. A reach this cannot follow - one
// built from a value, so that where it leads is not written down - is an
// error rather than a path passed over: the release would be published
// without whatever it names, and the first site to run it would find out.
func Reads(files map[string]string) ([]string, error) {
	seen := map[string]bool{ModulesDir: true}
	for rel, body := range files {
		if !within(ModulesDir, rel) || !strings.HasSuffix(rel, ".tf") {
			continue
		}
		for _, m := range reach.FindAllStringSubmatch(tofufiles.Code(body), -1) {
			if strings.Contains(m[2], "${") {
				return nil, fmt.Errorf("%s reaches outside its module through %q, and where that leads is not written in the path. Write the path whole, from the repository's top, so that what it names can be put in the release", rel, m[0])
			}
			to := path.Join(path.Dir(rel), strings.TrimPrefix(m[1], "/"), m[2])
			if strings.HasPrefix(to, "../") || to == ".." {
				return nil, fmt.Errorf("%s reaches %q, which is outside the repository", rel, m[0])
			}
			if to == "." {
				return nil, fmt.Errorf("%s reaches %q, which is the whole repository and names nothing a release could hold. Write the path to what is read", rel, m[0])
			}
			if !within(ModulesDir, to) {
				seen[to] = true
			}
		}
	}
	// A file inside a directory that is also read is already held.
	var out []string
	for p := range seen {
		covered := false
		for other := range seen {
			if other != p && !strings.ContainsAny(other, "*?[") && within(other, p) {
				covered = true
			}
		}
		if !covered {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

// within reports whether p is the path dir names or beneath it.
func within(dir, p string) bool { return p == dir || strings.HasPrefix(p, dir+"/") }

// Holds reports whether a release holding these paths carries a file.
func Holds(holds []string, file string) bool {
	for _, held := range holds {
		if strings.ContainsAny(held, "*?[") {
			if ok, err := path.Match(held, file); err == nil && ok {
				return true
			}
			continue
		}
		if within(held, file) {
			return true
		}
	}
	return false
}

// Files is the tracked files a release of the platform holds, as the
// manifest and git have them now: what the fabricator would pack from this
// checkout.
func Files(repoRoot string, tracked []string) ([]string, error) {
	release, err := Read(repoRoot)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, file := range tracked {
		if Holds(release.Holds, file) {
			out = append(out, file)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s holds no tracked file, so a release of it would be an empty package", Manifest)
	}
	sort.Strings(out)
	return out, nil
}
