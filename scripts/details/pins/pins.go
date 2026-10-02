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
// Being replaced: a site is moving to a published release of the platform,
// pinned by version (homelab/details/platform), and this file goes when the
// roots consume one.
package pins

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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
