package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// The kinds of work a job on the estate's own runners can be. The suppliers
// list says what each means, beside the entries that use them.
const (
	CannotWait = "cannot-wait"
	Short      = "short"
	CanWait    = "can-wait"
)

// ShortAtMost is the longest a job may take and still be short: what
// priority work arriving behind it would have to wait.
const ShortAtMost = 2 * time.Minute

// Work is what one job on the estate's runners says of itself: which kind of
// work it is, and how long it ordinarily takes.
type Work struct {
	// Job is the workflow's file and the job's key in it, as
	// "workflow.yml/job".
	Job   string `yaml:"job"`
	Kind  string `yaml:"kind"`
	Takes string `yaml:"takes"`
}

// Ordinarily is Takes as a duration.
func (w Work) Ordinarily() (time.Duration, error) {
	d, err := time.ParseDuration(w.Takes)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s says it takes %q, which is not a length of time", w.Job, w.Takes)
	}
	return d, nil
}

// DeclaredWork reads what each job on the estate's runners says of itself,
// from the suppliers list at the top of the repository. One reader, because
// the tier that holds a job to its declaration and the tier that holds the
// declaration to what really happened must be reading the same thing.
func DeclaredWork(repoRoot string) ([]Work, error) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, "scripts", "approved-suppliers.yml"))
	if err != nil {
		return nil, err
	}
	var file struct {
		Work []Work `yaml:"work"`
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("the suppliers list does not parse: %w", err)
	}
	for _, w := range file.Work {
		switch w.Kind {
		case CannotWait, Short, CanWait:
		default:
			return nil, fmt.Errorf("%s says it is %q work, which is not one of %s, %s or %s", w.Job, w.Kind, CannotWait, Short, CanWait)
		}
		if _, err := w.Ordinarily(); err != nil {
			return nil, err
		}
	}
	return file.Work, nil
}
