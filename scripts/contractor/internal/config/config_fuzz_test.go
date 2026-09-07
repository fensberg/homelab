package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The config is the one input the button cannot refuse to read.
//
// LoadRendered parses whatever `op inject` produced, and everything downstream
// - every site, every octet, every credential reference - comes out of it. A
// panic here is the start button dying before it has said anything useful,
// on a file the operator cannot see because it is gitignored by construction.
//
// Refusing a malformed config is always correct. Dying on one is not, and
// neither is accepting one and returning nothing for a caller to check.
func FuzzLoadRendered(f *testing.F) {
	for _, seed := range []string{
		`{}`,
		`{"sites":{}}`,
		`{"sites":{"site0":{"octet":10}}}`,
		`{"sites":{"site0":{"octet":"ten"}}}`,
		`{"sites":{"site0":{"octet":-1,"control_plane_count":0}}}`,
		`{"organization":"x","source_control":{},"sites":null}`,
		"", "{", "null", "[]", `{"sites":`,
		`{"sites":{"site0":{"octet":99999999999999999999}}}`,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, content string) {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Skip("could not write the fixture")
		}

		cfg, err := LoadRendered(path)
		if err != nil {
			// A refusal must not also hand back something to use. A caller
			// that checks the error and moves on is correct; one that does not
			// should get nothing rather than a half-built config.
			if cfg != nil {
				t.Errorf("LoadRendered refused %q and still returned a config", content)
			}
			return
		}
		if cfg == nil {
			t.Errorf("LoadRendered accepted %q and returned no config and no error, so a caller has nothing to check", content)
		}
	})
}
