// Package holds is what the estate holds that is worth keeping, and for how
// long: each asset's declaration, and the one question asked of it before
// anything is destroyed.
//
// Data is worth keeping for as long as the thing it describes is alive. A
// machine's readings can die with the machine; a site's history must not die
// when one machine does; a client's data outlives the estate. So an asset
// says the scope whose lifetime it has, the scope its working copy dies
// with, and where another copy is kept. Destroying something is safe for an
// asset unless the asset should outlive it and lives inside it - and then
// only a copy somewhere else, young enough, makes it safe.
//
// An application declares what it holds in its own declaration, and the core
// in a file of its own (CoreFile). The safety officer reads both and takes
// nobody's word for a copy: it looks.
package holds

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CoreFile is where the core says what it holds, from the top of the
// repository.
const CoreFile = "clusters/core/holds.json"

// Core is who an asset of the core belongs to, in the words a line about it
// uses.
const Core = "the core"

// Scope is something that can be destroyed, and so something an asset can
// live as long as. Each holds the ones before it.
type Scope int

const (
	Machine Scope = iota + 1
	Node
	Site
	Estate
	Client
)

var scopeNames = map[string]Scope{
	"machine": Machine, "node": Node, "site": Site, "estate": Estate, "client": Client,
}

// ScopeNamed reads a scope by its name in a declaration.
func ScopeNamed(name string) (Scope, bool) {
	s, ok := scopeNames[name]
	return s, ok
}

func (s Scope) String() string {
	for name, v := range scopeNames {
		if v == s {
			return name
		}
	}
	return "nothing"
}

// Asset is one thing worth keeping.
type Asset struct {
	// What it is, in the words a refusal should use.
	What string `json:"what"`
	// Lifetime is the scope it is worth keeping for as long as: machine,
	// node, site, estate, or client for what belongs to somebody else and
	// outlives the estate.
	Lifetime string `json:"lifetime"`
	// LivesOn is the scope its working copy dies with.
	LivesOn string `json:"lives_on"`
	// HeldBy is the object that declares its storage, as Kind/name: the
	// claim, or whatever carries a claim template.
	HeldBy string `json:"held_by"`
	// Copy is where another copy is kept. An asset with none leaves it out.
	Copy *Copy `json:"copy,omitempty"`
	// MayLose is how old the newest copy may be, as a duration. Said
	// whenever a copy is.
	MayLose string `json:"may_lose,omitempty"`

	// Owner is the application that declared it, or Core. Path is the
	// declaration.
	Owner string `json:"-"`
	Path  string `json:"-"`
}

// Copy is where an asset's other copy is kept, in object storage.
type Copy struct {
	// Storage is which of the site's buckets, by purpose. The core says;
	// an application leaves it out, because its bucket is the one for the
	// environment a site runs it in.
	Storage string `json:"storage,omitempty"`
	// Under is the folder of the bucket the copies are in.
	Under string `json:"under"`
}

// Check refuses a declaration that leaves out what the question needs.
func (a Asset) Check() error {
	for field, value := range map[string]string{"what": a.What, "held_by": a.HeldBy} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s holds something and leaves its %s empty", a.Path, field)
		}
	}
	if kind, name, ok := strings.Cut(a.HeldBy, "/"); !ok || kind == "" || name == "" {
		return fmt.Errorf("%s says %s is held by %q, which is not a Kind/name", a.Path, a.What, a.HeldBy)
	}
	lifetime, ok := ScopeNamed(a.Lifetime)
	if !ok {
		return fmt.Errorf("%s gives %s the lifetime %q, which is not a scope", a.Path, a.What, a.Lifetime)
	}
	livesOn, ok := ScopeNamed(a.LivesOn)
	if !ok || livesOn == Client {
		return fmt.Errorf("%s says %s lives on %q, which is not something of the estate's that can be destroyed", a.Path, a.What, a.LivesOn)
	}
	if lifetime < livesOn {
		return fmt.Errorf("%s says %s is worth keeping for the life of a %s and lives on a %s, which outlasts it", a.Path, a.What, lifetime, livesOn)
	}
	switch {
	case a.Copy == nil && a.MayLose != "":
		return fmt.Errorf("%s says how much of %s may be lost and names no copy to measure", a.Path, a.What)
	case a.Copy == nil:
		return nil
	case strings.Trim(a.Copy.Under, "/") == "":
		return fmt.Errorf("%s says %s has a copy and not which folder it is under", a.Path, a.What)
	case a.Owner == Core && a.Copy.Storage == "":
		return fmt.Errorf("%s says %s has a copy and not which bucket it is in", a.Path, a.What)
	case a.Owner != Core && a.Copy.Storage != "":
		return fmt.Errorf("%s names the bucket %s is copied to; an application's bucket is the one for the environment a site runs it in", a.Path, a.What)
	}
	if _, err := a.Tolerance(); err != nil {
		return err
	}
	return nil
}

// Tolerance is MayLose as a duration.
func (a Asset) Tolerance() (time.Duration, error) {
	d, err := time.ParseDuration(a.MayLose)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s says %s has a copy and gives no age its newest may be (may_lose %q)", a.Path, a.What, a.MayLose)
	}
	return d, nil
}

// EndangeredBy says whether destroying something of that scope could lose
// the asset: it should outlive it, and its working copy is inside it.
func (a Asset) EndangeredBy(destroying Scope) bool {
	lifetime, _ := ScopeNamed(a.Lifetime)
	livesOn, _ := ScopeNamed(a.LivesOn)
	return lifetime > destroying && livesOn <= destroying
}

// Owned stamps a list of assets with who declared them and where, and checks
// each.
func Owned(assets []Asset, owner, path string) ([]Asset, error) {
	out := make([]Asset, 0, len(assets))
	for _, a := range assets {
		a.Owner, a.Path = owner, path
		if err := a.Check(); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// ReadCore reads what the core holds. A repository with no such file has a
// core that says it holds nothing, which the guards over storage then judge.
func ReadCore(repoRoot string) ([]Asset, error) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(CoreFile)))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseCore(raw)
}

// ParseCore reads the core's declaration.
func ParseCore(raw []byte) ([]Asset, error) {
	var file struct {
		Comment []string `json:"_comment,omitempty"`
		Holds   []Asset  `json:"holds"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("%s is not a declaration this reads: %w", CoreFile, err)
	}
	return Owned(file.Holds, Core, CoreFile)
}
