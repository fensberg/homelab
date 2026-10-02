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

// Order is the name the platform is published under: the work order that
// packs it, and so the package <repository>-<Order>-release.
const Order = "platform"

// VersionsFile is where each site says which version of the platform it
// runs, from the top of the repository: one line per site.
//
// Under management/, beside the roots, because that is what a converge
// applies: a pull request that changes a site's line is planned with the
// version it would leave the site on, the merge converges the site, and
// nothing else in the repository changes what a site runs.
const VersionsFile = "management/versions.json"

// Pin is the release a site runs: the version, which is for people, and the
// digest of the package published under it, which is what is fetched. A
// version is a name in a registry and a name can be made to point elsewhere;
// a digest cannot.
type Pin struct{ Version, Digest string }

// pinned is how a site's line is written: v2026.10.1@sha256:<64 hex>. One
// string, so that moving a site from one release to the next is one line.
var pinned = regexp.MustCompile(`^(v[0-9][0-9.]*)@(sha256:[0-9a-f]{64})$`)

// ParsePin reads one site's line.
func ParsePin(line string) (Pin, error) {
	m := pinned.FindStringSubmatch(line)
	if m == nil || !Version.MatchString(m[1]) {
		return Pin{}, fmt.Errorf("%q is not a release of the platform: a version and the digest it was published as, v2026.10.1@sha256:<64 hex>", line)
	}
	return Pin{Version: m[1], Digest: m[2]}, nil
}

// Pins reads every site's line.
func Pins(repoRoot string) (map[string]Pin, error) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(VersionsFile)))
	if err != nil {
		return nil, fmt.Errorf("reading which version of the platform each site runs: %w", err)
	}
	var sites map[string]struct {
		Platform string `json:"platform"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sites); err != nil {
		return nil, fmt.Errorf("%s is not what this reads - each site, and the platform release it runs: %w", VersionsFile, err)
	}
	out := map[string]Pin{}
	for site, s := range sites {
		pin, err := ParsePin(s.Platform)
		if err != nil {
			return nil, fmt.Errorf("%s, %s: %w", VersionsFile, site, err)
		}
		out[site] = pin
	}
	return out, nil
}

// Pinned is the release one site runs. A site with no line runs nothing:
// there is no default, because a site that was not told which version to
// run would otherwise run whichever was newest on the day it was converged.
func Pinned(repoRoot, site string) (Pin, error) {
	pins, err := Pins(repoRoot)
	if err != nil {
		return Pin{}, err
	}
	pin, ok := pins[site]
	if !ok {
		return Pin{}, fmt.Errorf("%s says nothing of %s, so there is no version of the platform for it to run. Give it a line", VersionsFile, site)
	}
	return pin, nil
}

// Registry is where the platform's releases are published for a repository
// (owner/name): the address a root fetches its modules from. Lower case,
// because the registry is.
func Registry(repository string) string {
	return "ghcr.io/" + strings.ToLower(repository) + "-" + Order + "-release"
}

// The variables a site's root is told its modules by, as tofu reads them
// from the environment, and the one that says where tofu's own settings are.
const (
	// ReleaseVariable carries the registry address, and DigestVariable the
	// digest of the release to fetch from it.
	ReleaseVariable = "TF_VAR_release"
	DigestVariable  = "TF_VAR_digest"
	// UnreleasedVariable carries, instead, the path of a tree holding the
	// modules as they are in a checkout. For a check that has to see a
	// change before it is released, and never for a run against a site.
	UnreleasedVariable = "TF_VAR_unreleased"
	// CLIConfigVariable names the file tofu reads its registry credential
	// from.
	CLIConfigVariable = "TF_CLI_CONFIG_FILE"
)

// RegistryHost is the registry the credential is for.
const RegistryHost = "ghcr.io"

// CLIConfig is the settings file that lets tofu fetch a release: a credential
// for the registry. The registry refuses tofu's anonymous request even for a
// public package, so every fetch presents a token; any token GitHub issued
// will do, since the package is public, and it grants nothing the reader did
// not already have.
func CLIConfig(token string) ([]byte, error) {
	token = strings.TrimSpace(token)
	if token == "" || strings.ContainsAny(token, "\"\\\n\r") {
		return nil, fmt.Errorf("the registry credential is empty or is not a token")
	}
	return []byte("oci_credentials \"" + RegistryHost + "\" {\n  username = \"x-access-token\"\n  password = \"" + token + "\"\n}\n"), nil
}

// Unreleased is where a checkout's own copy of what a release would hold is
// put, from the top of the repository: ignored by git, and what a root's
// unreleased variable names.
const Unreleased = ".unreleased"

// PlaceUnreleased makes that tree from the tracked files the release manifest
// holds, as they are in this checkout, and nothing else of the repository. A
// root planned against it is planned against what would be published, so a
// module that reads a file the release does not hold fails in the check and
// not at the first site to run it. What was there before is replaced whole.
func PlaceUnreleased(repoRoot string, tracked []string) (string, error) {
	files, err := Files(repoRoot, tracked)
	if err != nil {
		return "", err
	}
	target := filepath.Join(repoRoot, Unreleased)
	if err := os.RemoveAll(target); err != nil {
		return "", err
	}
	for _, rel := range files {
		body, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		dst := filepath.Join(target, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(dst, body, 0o644); err != nil {
			return "", err
		}
	}
	return target, nil
}
