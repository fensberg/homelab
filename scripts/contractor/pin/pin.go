// Package pin is which version of the estate's modules each site runs, and
// how a site's roots are handed exactly that version.
//
// A site's roots build nothing themselves; they call modules
// (modules/infrastructure/). Called by path, every site would run the modules
// as they stand on whatever commit is being converged, so a change would
// reach every site on the merge that made it. A pin is what lets it reach one
// site first: each site runs the modules as they were at a commit named in
// management/pins.json, the estate's default or the site's own, and moving a site
// is changing that line.
//
// The root does not fetch anything. Before any tofu run, the contractor puts
// the repository as it was at the site's pinned commit into .pinned/<site>/,
// and the root's module source points there. OpenTofu's own git source was
// the other way to do this, and was not taken: it sees only branches and
// tags, so a pin naming a commit a squash merge has orphaned could not be
// fetched; it would write the repository's address into the code; and it
// needs the network at every init. This fetches a commit by its hash when the
// checkout lacks it, and otherwise needs nothing.
//
// What a pin carries is the whole tree, not the modules alone: a module reads
// the tunnel routes, the address plan and the manifests it bootstraps Flux
// from, and those have to be the ones it was written against.
package pin

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"homelab/details/gitenv"
)

// File is where the pins are, from the top of the repository.
//
// Under management/, beside the roots, because that is what a converge
// applies: a merge that changes this file is planned on its pull request,
// converged when it lands, and returned with the rest of management/ if that
// converge fails. Anywhere else, moving a pin would be a change the estate's
// own machinery did not see.
const File = "management/pins.json"

// dir is where pinned trees are put, from the top of the repository: one per
// site, ignored by git.
const dir = ".pinned"

// marker is the file in a pinned tree that says which commit it is of, so a
// tree already in place is not extracted again.
const marker = ".pinned-commit"

// Pins is the estate's default and each site's own.
type Pins struct {
	// Default is the commit a site runs unless it names another. A site
	// nobody has pinned runs this, so bringing one online needs no pin.
	Default string `json:"default"`
	// Sites holds a commit for each site held at a version of its own,
	// ahead of the default or behind it.
	Sites map[string]string `json:"per_site"`
}

// Read loads the pins and refuses any that is not a full commit hash: a
// branch or a tag names whatever it points at today, which is not a pin.
func Read(repoRoot string) (Pins, error) {
	var p Pins
	raw, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(File)))
	if err != nil {
		return p, fmt.Errorf("reading the pins: %w", err)
	}
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

// Dir is where a site's pinned tree is, which is what its roots' module
// sources point into.
func Dir(repoRoot, site string) string {
	return filepath.Join(repoRoot, dir, site)
}

// Git runs git in the repository and returns its output. A parameter so a
// test can say what the repository holds.
type Git func(repoRoot string, args ...string) ([]byte, error)

// Exec is the Git every caller outside a test uses.
func Exec(repoRoot string, args ...string) ([]byte, error) {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// Place puts the repository as it was at the site's pinned commit where the
// site's roots read their modules from, and returns the commit.
//
// It is idempotent: a tree already of that commit is left alone, and a tree
// of another commit is replaced whole - a site must never run a mixture of
// two versions.
func Place(repoRoot, site string, git Git) (string, error) {
	if site == WorkingTree || site == "" {
		return "", fmt.Errorf("%q is not a site to pin: that name is the working tree's", site)
	}
	pins, err := Read(repoRoot)
	if err != nil {
		return "", err
	}
	sha := pins.For(site)
	target := Dir(repoRoot, site)
	if placed, err := os.ReadFile(filepath.Join(target, marker)); err == nil && strings.TrimSpace(string(placed)) == sha {
		return sha, nil
	}

	// A checkout made for one commit does not hold every other: a runner's
	// is shallow, and a commit a squash merge left behind is on no branch.
	if _, err := git(repoRoot, "cat-file", "-e", sha+"^{commit}"); err != nil {
		if _, err := git(repoRoot, "fetch", "--quiet", "--depth", "1", "origin", sha); err != nil {
			return "", fmt.Errorf("%s is pinned to %s, which this checkout does not hold and which could not be fetched: %w", site, sha, err)
		}
	}
	archive, err := git(repoRoot, "archive", "--format=tar", sha)
	if err != nil {
		return "", fmt.Errorf("reading the tree at %s: %w", sha, err)
	}

	// Extracted beside the target and moved into place, so a run that stops
	// partway leaves the old tree or none, never half of the new one.
	staging := target + ".incoming"
	if err := os.RemoveAll(staging); err != nil {
		return "", err
	}
	if err := extract(archive, staging); err != nil {
		_ = os.RemoveAll(staging)
		return "", fmt.Errorf("extracting the tree at %s: %w", sha, err)
	}
	if err := os.WriteFile(filepath.Join(staging, marker), []byte(sha+"\n"), 0o644); err != nil {
		return "", err
	}
	if err := os.RemoveAll(target); err != nil {
		return "", err
	}
	if err := os.Rename(staging, target); err != nil {
		return "", err
	}
	return sha, nil
}

// WorkingTree is the name of the tree that is the working tree itself: what a
// root is told to read its modules from by a check that has to see a change
// before it merges. It is never a site's, so a check and a run against an
// estate cannot take each other's tree.
const WorkingTree = "worktree"

// TreeVariable is the root variable that says which tree to read modules
// from, as tofu reads it from the environment.
const TreeVariable = "TF_VAR_tree"

// PlaceWorkingTree makes the tree named WorkingTree: a link to the
// repository itself. It is for validating a root and planning it from
// nothing, and never for a run against an estate, which names its site.
func PlaceWorkingTree(repoRoot string) error {
	target := Dir(repoRoot, WorkingTree)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	// Relative, so the link holds wherever the repository is checked out.
	return os.Symlink("..", target)
}

// largestFile bounds one extracted file. The largest this repository tracks
// is Flux's generated install at about a megabyte; this leaves room for it to
// grow many times over and still refuses an archive that is not a source tree.
const largestFile = 64 << 20

// extract writes a tar archive of a tree under dst. Only what a git tree
// holds is written - directories, files and links - and nothing outside dst.
func extract(archive []byte, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(bytes.NewReader(archive))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		path := filepath.Join(dst, filepath.FromSlash(h.Name))
		if rel, err := filepath.Rel(dst, path); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("the archive names %q, which is outside the tree", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				mode = 0o755
			}
			f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			// Exactly as many bytes as the archive says the file has, and
			// never more than a file of this repository could be: what is
			// extracted is bounded by the archive's own header rather than
			// by however much the stream turns out to hold.
			if h.Size < 0 || h.Size > largestFile {
				f.Close()
				return fmt.Errorf("the archive holds %q at %d bytes, which is more than any file of this repository", h.Name, h.Size)
			}
			if _, err := io.CopyN(f, tr, h.Size); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(h.Linkname, path); err != nil {
				return err
			}
		case tar.TypeXGlobalHeader:
			// git's note of which commit the archive is of.
		default:
			return fmt.Errorf("the archive holds %q, which is not a file, a directory or a link", h.Name)
		}
	}
}
