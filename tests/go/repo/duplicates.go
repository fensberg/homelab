// Package repo holds checks about this repository's own files, rather than
// about the infrastructure it describes.
//
// It exists because of a specific class of defect: a key written twice in one
// file, where the parser silently keeps the last one and reports nothing.
// Every format this project configures itself in behaves that way. An
// env-file read by `docker --env-file` takes the last assignment. A JSON
// object decoded by encoding/json takes the last member. A YAML mapping
// loaded by PyYAML - which is what pre-commit's check-yaml uses - takes the
// last entry. None of them warn, so no formatter, linter or schema validator
// in this pipeline has anything to say about it.
//
// That is not hypothetical. A merge produced a duplicated
// VALIDATE_TERRAGRUNT in .github/super-linter.vars and every one of the
// eleven pre-commit hooks passed the file; it was found by diffing two
// resolutions of the same merge against each other. This package is what
// stops the next one needing that kind of luck.
package repo

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Duplicate is one key that appears more than once at the same level.
type Duplicate struct {
	Path  string // dotted path to the containing object, "" at the top level
	Key   string
	Count int
}

func (d Duplicate) String() string {
	where := d.Path
	if where == "" {
		where = "(top level)"
	}
	return fmt.Sprintf("%s: %q appears %d times in %s", d.Path, d.Key, d.Count, where)
}

func collect(seen map[string]int, path string, out *[]Duplicate) {
	keys := make([]string, 0, len(seen))
	for k, n := range seen {
		if n > 1 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		*out = append(*out, Duplicate{Path: path, Key: k, Count: seen[k]})
	}
}

// --- env files --------------------------------------------------------------

var envAssignment = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=`)

// EnvDuplicates finds keys assigned more than once in a KEY=VALUE file.
// Comments and blank lines are ignored; so is anything that is not an
// assignment, since these files also carry prose.
func EnvDuplicates(content string) []Duplicate {
	seen := map[string]int{}
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if m := envAssignment.FindStringSubmatch(line); m != nil {
			seen[m[1]]++
		}
	}
	var out []Duplicate
	collect(seen, "", &out)
	return out
}

// --- JSON -------------------------------------------------------------------

// JSONDuplicates walks the token stream rather than unmarshalling, because
// unmarshalling is exactly the step that discards the evidence: by the time
// there is a map[string]any, the earlier value is already gone.
func JSONDuplicates(content string) ([]Duplicate, error) {
	dec := json.NewDecoder(strings.NewReader(content))
	var out []Duplicate
	if err := jsonValue(dec, "", &out); err != nil && err != io.EOF {
		return nil, err
	}
	return out, nil
}

func jsonValue(dec *json.Decoder, path string, out *[]Duplicate) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil // scalar
	}

	switch delim {
	case '{':
		seen := map[string]int{}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyTok.(string)
			if !ok {
				return fmt.Errorf("object key was not a string at %s", path)
			}
			seen[key]++
			child := key
			if path != "" {
				child = path + "." + key
			}
			if err := jsonValue(dec, child, out); err != nil {
				return err
			}
		}
		collect(seen, path, out)
	case '[':
		for i := 0; dec.More(); i++ {
			if err := jsonValue(dec, fmt.Sprintf("%s[%d]", path, i), out); err != nil {
				return err
			}
		}
	}
	// consume the closing delimiter
	_, err = dec.Token()
	return err
}

// --- YAML -------------------------------------------------------------------

// YAMLDuplicates handles multi-document files, which Kubernetes manifests
// legitimately are.
func YAMLDuplicates(content string) ([]Duplicate, error) {
	dec := yaml.NewDecoder(strings.NewReader(content))
	var out []Duplicate
	for i := 0; ; i++ {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		prefix := ""
		if i > 0 {
			prefix = fmt.Sprintf("doc[%d]", i)
		}
		yamlNode(&doc, prefix, &out)
	}
}

func yamlNode(n *yaml.Node, path string, out *[]Duplicate) {
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			yamlNode(c, path, out)
		}
	case yaml.MappingNode:
		seen := map[string]int{}
		// Mapping content alternates key, value, key, value.
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i].Value
			seen[key]++
			child := key
			if path != "" {
				child = path + "." + key
			}
			yamlNode(n.Content[i+1], child, out)
		}
		collect(seen, path, out)
	case yaml.SequenceNode:
		for i, c := range n.Content {
			yamlNode(c, fmt.Sprintf("%s[%d]", path, i), out)
		}
	}
}

// --- reading ----------------------------------------------------------------

// Check dispatches on file extension and returns whatever duplicates the
// matching parser found. An unrecognised extension is not an error - it is a
// file this check has nothing to say about.
func Check(path string) ([]Duplicate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	content := string(data)

	switch {
	case strings.HasSuffix(path, ".json"):
		return JSONDuplicates(content)
	case strings.HasSuffix(path, ".yaml"), strings.HasSuffix(path, ".yml"):
		return YAMLDuplicates(content)
	case strings.HasSuffix(path, ".vars"), strings.HasSuffix(path, ".env"):
		return EnvDuplicates(content), nil
	default:
		return nil, nil
	}
}
