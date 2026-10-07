// Package sizing is where each size in the estate came from, said as data.
//
// A workload's reservation is what it declares it needs, in its manifest,
// which is Kubernetes' own way of saying so. What a manifest cannot say is
// how anybody arrived at the number - and that used to be a comment beside
// it, which nothing reads and nothing checks. The difference matters: a
// site is packed tight on these figures, and a figure somebody guessed must
// not be mistaken for one that was seen under load.
//
// So each thing that reserves says which of three its figures are. An
// application says so in its declaration, and the core in a file of its own
// (CoreFile).
package sizing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CoreFile is where the core says where its sizes came from, from the top of
// the repository.
const CoreFile = "clusters/core/sized-from.json"

// EveryPod is the name, in place of a container's, for everything a release
// makes: a chart's pods are its own to name.
const EveryPod = "*"

// The three places a size can have come from.
const (
	// Publisher: whoever makes the software says it needs this.
	Publisher = "publisher"
	// Estimate: somebody here chose it, and it has not been seen under
	// load. Most sizes start here, and saying so is the point.
	Estimate = "estimate"
	// Measured: it was watched doing its work, and this is what it used.
	Measured = "measured"
)

// Source is where one thing's sizes came from.
type Source struct {
	// Kind is one of Publisher, Estimate and Measured.
	Kind string `json:"kind"`
	// Detail is the particulars: which guidance, or what the estimate was
	// reasoned from.
	Detail string `json:"basis"`
	// Over is how long it was watched for, as a duration, and Load what it
	// was doing while it was watched. Both are said for a size that was
	// measured and for no other: a reading from a thing that sat idle is
	// not a measurement of what it needs.
	Over string `json:"over,omitempty"`
	Load string `json:"load,omitempty"`
}

// Check refuses a source that does not say enough to be believed.
func (s Source) Check(of string) error {
	if strings.TrimSpace(s.Detail) == "" {
		return fmt.Errorf("%s says where its size came from and gives no detail", of)
	}
	switch s.Kind {
	case Publisher, Estimate:
		if s.Over != "" || s.Load != "" {
			return fmt.Errorf("%s is sized from a %s's figure and says how long it was watched; only a measured size was watched", of, s.Kind)
		}
	case Measured:
		if d, err := time.ParseDuration(s.Over); err != nil || d < 24*time.Hour {
			return fmt.Errorf("%s says its size was measured and not over how long (over %q): a measurement names a window of a day or more", of, s.Over)
		}
		if strings.TrimSpace(s.Load) == "" {
			return fmt.Errorf("%s says its size was measured and not what it was doing meanwhile: a reading from a thing that sat idle is not a measurement of what it needs", of)
		}
	default:
		return fmt.Errorf("%s says its size came from %q, which is not one of %s, %s or %s", of, s.Kind, Publisher, Estimate, Measured)
	}
	return nil
}

// Declared is where each thing's sizes came from: by the object that
// reserves, as Kind/name, and then by the container in it, or EveryPod.
type Declared map[string]map[string]Source

// Check refuses a declaration with an entry that cannot be believed.
func (d Declared) Check(path string) error {
	for object, containers := range d {
		if kind, name, ok := strings.Cut(object, "/"); !ok || kind == "" || name == "" {
			return fmt.Errorf("%s names %q, which is not a Kind/name", path, object)
		}
		if len(containers) == 0 {
			return fmt.Errorf("%s names %s and says nothing about it", path, object)
		}
		for container, source := range containers {
			if err := source.Check(fmt.Sprintf("%s: %s, container %s,", path, object, container)); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReadCore reads where the core's sizes came from. A repository with no such
// file has a core that says nothing, which the guard over what reserves then
// judges.
func ReadCore(repoRoot string) (Declared, error) {
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
func ParseCore(raw []byte) (Declared, error) {
	var file struct {
		Comment   []string `json:"_comment,omitempty"`
		SizedFrom Declared `json:"sized_from"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("%s is not a declaration this reads: %w", CoreFile, err)
	}
	return file.SizedFrom, file.SizedFrom.Check(CoreFile)
}
