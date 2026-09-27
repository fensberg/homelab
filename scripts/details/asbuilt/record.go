package asbuilt

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// The files a record is made of. A directory rather than one document, so
// the state is exactly the file tofu reads and nothing has to take it apart.
const (
	stateFile  = "terraform.tfstate"
	configFile = "config.json"
	metaFile   = "record.json"
)

// Meta says what a record is of: which site, which commit of main the estate
// had converged to, and when it was taken. It is what a plan comment names,
// so a reader can tell how old the ground truth is.
type Meta struct {
	Site   string    `json:"site"`
	Commit string    `json:"commit"`
	Taken  time.Time `json:"taken"`
}

// Write puts a publishable record in dir. It refuses one that is not: a
// record that is not quiet makes every plan against it show changes that are
// not real, and one that still holds a real value must not leave the machine.
func Write(dir string, res *Result, meta Meta) error {
	if !res.Publishable() {
		return errors.New("this record is not publishable, so it is not written")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for name, v := range map[string]any{stateFile: res.State, configFile: res.Config, metaFile: meta} {
		if err := writeJSON(filepath.Join(dir, name), v); err != nil {
			return err
		}
	}
	return nil
}

// Read loads a record written by Write.
func Read(dir string) (state, config map[string]any, meta Meta, err error) {
	docs := map[string]map[string]any{}
	for _, name := range []string{stateFile, configFile} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, nil, meta, fmt.Errorf("the record at %s is incomplete: %w", dir, err)
		}
		if docs[name], err = decode(raw); err != nil {
			return nil, nil, meta, fmt.Errorf("the record's %s is not JSON: %w", name, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, metaFile))
	if err != nil {
		return nil, nil, meta, fmt.Errorf("the record at %s says nothing about what it is of: %w", dir, err)
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, nil, meta, fmt.Errorf("the record's %s is not JSON: %w", metaFile, err)
	}
	return docs[stateFile], docs[configFile], meta, nil
}
