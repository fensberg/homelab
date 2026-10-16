package repo

import (
	"errors"
	"io"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// mappingValue is the value under key in a YAML mapping, or nil.
func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// document is the one document of a manifest that declares an object.
func document(t *testing.T, kind, name string) (path string, doc map[string]any) {
	t.Helper()
	path, body := fluxObject(t, kind, name)
	dec := yaml.NewDecoder(strings.NewReader(body))
	for {
		var each map[string]any
		err := dec.Decode(&each)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("%s declares the %s %s and is not YAML that can be read: %v", path, kind, name, err)
		}
		meta, _ := each["metadata"].(map[string]any)
		if each["kind"] == kind && meta["name"] == name {
			return path, each
		}
	}
	t.Fatalf("%s declares the %s %s and it could not be read out of it", path, kind, name)
	return "", nil
}
