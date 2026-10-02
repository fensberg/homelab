// Package workorders is what the fabricator builds, and for an order that
// makes a release, what the release is made of.
//
// The estate's own orders are in scripts/work-orders.json. Every application
// that has an image is an order too, by being there: named for its directory,
// built from image/ inside it, and released as its declaration says
// (homelab/details/applications). The fabricator composes the two in its
// workflow; the superintendent judges a delivery by the same orders and the
// test tier holds the fabricator to them, so the composition is written here
// once for every program.
package workorders

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"homelab/details/applications"
)

// Path is where the estate's own work orders live, from the repository root.
const Path = "scripts/work-orders.json"

// Order is one thing the fabricator builds.
type Order struct {
	Name string `json:"name"`
	// The build context, holding the Dockerfile. Empty for an order that
	// builds no image.
	Context string `json:"context,omitempty"`
	// The manifest of a package the order publishes: a set of the
	// repository's own files, released whole under a version. Empty for an
	// order that publishes none.
	Package string `json:"package,omitempty"`
	// The order's own pins file, beside the estate's. Empty for an order
	// pinned by the estate's alone.
	Pins    string   `json:"pins,omitempty"`
	Release *Release `json:"release,omitempty"`
}

// Release is how an order's release is made: the directory that is staged,
// whole and alone, and how the version is read from the built image.
type Release struct {
	Module  string `json:"module"`
	Version struct {
		Env     []string `json:"env"`
		Pattern string   `json:"pattern"`
		Example struct {
			Line    string `json:"line"`
			Version string `json:"version"`
		} `json:"example"`
	} `json:"version"`
}

// Parse reads the estate's own orders. A file with none is refused: an empty
// list builds nothing, and says so the same way a correct one does.
func Parse(body []byte) ([]Order, error) {
	var f struct {
		Orders []Order `json:"orders"`
	}
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("reading %s: %w", Path, err)
	}
	if len(f.Orders) == 0 {
		return nil, fmt.Errorf("%s holds no orders", Path)
	}
	for _, o := range f.Orders {
		// An order builds an image or publishes a package: one of them, so
		// that what it is for is never a guess.
		if o.Name == "" || (o.Context == "") == (o.Package == "") {
			return nil, fmt.Errorf("%s: the order %q must have a name and exactly one of a context, to build an image from, and a package, to publish", Path, o.Name)
		}
	}
	return f.Orders, nil
}

// For is the order an application is, given that it has an image.
func For(a applications.Application) Order {
	o := Order{Name: a.Name, Context: a.Image(), Pins: a.PinsFile()}
	if a.Release != nil {
		o.Release = &Release{Module: a.Root}
		o.Release.Version = a.Release.Version
	}
	return o
}

// Read is every order the fabricator builds: the estate's own, then one for
// each application that has an image, in name order.
func Read(repoRoot string) ([]Order, error) {
	body, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(Path)))
	if err != nil {
		return nil, err
	}
	orders, err := Parse(body)
	if err != nil {
		return nil, err
	}
	apps, err := applications.Read(repoRoot)
	if err != nil {
		return nil, err
	}
	for _, a := range apps {
		if a.Builds(repoRoot) {
			orders = append(orders, For(a))
		}
	}
	return orders, nil
}
