package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// TeardownDeclaration is the file a workload says how to back itself up in,
// in its own directory under modules/applications/. Read from the repository
// rather than from the running pod, because what runs in production is a
// release, and a declaration that had to be released before it could be read
// would not protect the teardown that is about to happen.
const TeardownDeclaration = "before-teardown.json"

// ApplicationsDir is where workloads are, from the top of the repository: one
// directory each, holding everything that is that workload's.
const ApplicationsDir = "modules/applications"

// BeforeTeardown is what one workload declares: how to take a backup now,
// before the site it runs on is destroyed (#588).
type BeforeTeardown struct {
	// What is being saved, in the words a progress line should use.
	What string `json:"what"`
	// Where the workload's pods are, and how to pick them.
	Namespace string `json:"namespace"`
	Selector  string `json:"selector"`
	// The container to run the command in, and the command. It takes a
	// backup now and exits zero only when it has.
	Container string   `json:"container"`
	Command   []string `json:"command"`

	// Workload is the directory the declaration was found in, and Path the
	// declaration itself, from the top of the repository.
	Workload string `json:"-"`
	Path     string `json:"-"`
}

// DeclaredBackups reads every workload's declaration. One that does not parse
// or leaves a field out is an error, never a workload with nothing to save: a
// typo must not read as "nothing to lose".
func DeclaredBackups(repoRoot string) ([]BeforeTeardown, error) {
	found, err := filepath.Glob(filepath.Join(repoRoot, filepath.FromSlash(ApplicationsDir), "*", TeardownDeclaration))
	if err != nil {
		return nil, err
	}
	sort.Strings(found)
	var out []BeforeTeardown
	for _, path := range found {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var d BeforeTeardown
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&d); err != nil {
			return nil, fmt.Errorf("%s does not say how to back the workload up: %w", path, err)
		}
		d.Workload = filepath.Base(filepath.Dir(path))
		if rel, err := filepath.Rel(repoRoot, path); err == nil {
			d.Path = filepath.ToSlash(rel)
		}
		for field, value := range map[string]string{"what": d.What, "namespace": d.Namespace, "selector": d.Selector, "container": d.Container} {
			if strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("%s leaves %q empty, so there is no telling what to back up or where", path, field)
			}
		}
		if len(d.Command) == 0 {
			return nil, fmt.Errorf("%s names no command, so there is nothing to run that would take the backup", path)
		}
		out = append(out, d)
	}
	return out, nil
}
