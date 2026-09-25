// Package tofustate reads the parts of an OpenTofu state file the programs
// here make decisions on. The contractor's restore checks that what came back
// is state with something in it; the lawyer asks what the estate holds. Both
// are reading one format, so it is read in one place.
package tofustate

import "encoding/json"

// State is a state file, as far as anything here needs it.
type State struct {
	Version   int        `json:"version"`
	Serial    int        `json:"serial"`
	Lineage   string     `json:"lineage"`
	Resources []Resource `json:"resources"`
}

// Resource is one entry of the resources list: a managed resource or a data
// source, with all of its instances.
type Resource struct {
	Mode string `json:"mode"`
	Type string `json:"type"`
	Name string `json:"name"`
}

// Parse reads a state file. It says nothing about whether the result is
// sensible; each caller knows what it needs to be true.
func Parse(body []byte) (State, error) {
	var st State
	err := json.Unmarshal(body, &st)
	return st, err
}

// Managed names the managed resources, type.name, leaving out data sources:
// a data source is read on every plan and owns nothing.
func (s State) Managed() []string {
	var addrs []string
	for _, r := range s.Resources {
		if r.Mode == "managed" {
			addrs = append(addrs, r.Type+"."+r.Name)
		}
	}
	return addrs
}
