// Package workorders reads scripts/work-orders.json: what the fabricator
// builds, and for an order that makes a release, what the release is made of.
// The superintendent judges a delivery by those inputs and the test tier holds
// the fabricator to them, so the file is read in one place.
package workorders

import (
	"encoding/json"
	"fmt"
)

// Path is where the work orders live, from the repository root.
const Path = "scripts/work-orders.json"

// Order is one thing the fabricator builds.
type Order struct {
	Name string `json:"name"`
	// The build context, holding the Dockerfile.
	Context string   `json:"context"`
	Release *Release `json:"release"`
}

// Release is how an order's release is made: the production overlay and the
// module it builds on, and how the version is read from the built image.
type Release struct {
	Overlay string `json:"overlay"`
	Module  string `json:"module"`
	Version struct {
		Env     []string `json:"env"`
		Pattern string   `json:"pattern"`
	} `json:"version"`
}

// Parse reads the file's orders. A file with none is refused: an empty list
// builds nothing, and says so the same way a correct one does.
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
	return f.Orders, nil
}
