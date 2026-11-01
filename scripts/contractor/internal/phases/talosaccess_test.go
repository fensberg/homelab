package phases

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"homelab/contractor/internal/run"
)

// The Talos client configuration is kept in the site's own vault, under the
// one item and field the roll reads, base64 on one line as the vault keeps
// every credential; and a configuration that cannot be read, or a vault that
// cannot be written, stops the converge rather than leaving the roll with an
// old one.
func TestTheTalosAccessIsKeptInTheSitesVaultForTheRoll(t *testing.T) {
	ctx := &run.Context{Site: "north"}
	const config = "context: north\ncontexts:\n  north:\n    nodes: [10.0.0.1]\n"
	var vault, title string
	var fields map[string]string
	err := keepTalosAccess(ctx, func() (string, error) { return config, nil },
		func(v, ti string, f map[string]string) ([]string, error) {
			vault, title, fields = v, ti, f
			return []string{TalosAccessField}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if vault != "north" || title != TalosAccessItem || len(fields) != 1 {
		t.Fatalf("kept in %s/%s as %v", vault, title, fields)
	}
	decoded, err := base64.StdEncoding.DecodeString(fields[TalosAccessField])
	if err != nil || string(decoded) != config || strings.Contains(fields[TalosAccessField], "\n") {
		t.Errorf("the configuration was not kept as itself, base64 on one line: %q", fields[TalosAccessField])
	}

	unread := keepTalosAccess(ctx, func() (string, error) { return "", errors.New("no state") }, func(string, string, map[string]string) ([]string, error) {
		t.Error("a configuration that could not be read was written")
		return nil, nil
	})
	if unread == nil {
		t.Error("a configuration that could not be read was passed over")
	}
	for name, wrong := range map[string]string{
		"a diagnostic": "Warning: No outputs found",
		"no nodes":     "context: north\ncontexts:\n  north:\n    nodes: []\n",
	} {
		if keepTalosAccess(ctx, func() (string, error) { return wrong, nil }, func(string, string, map[string]string) ([]string, error) {
			t.Errorf("%s was kept as the configuration", name)
			return nil, nil
		}) == nil {
			t.Errorf("%s was passed over", name)
		}
	}
	unwritten := keepTalosAccess(ctx, func() (string, error) { return config, nil },
		func(string, string, map[string]string) ([]string, error) { return nil, errors.New("vault refused") })
	if unwritten == nil {
		t.Error("a vault that would not take the configuration was passed over")
	}
}
