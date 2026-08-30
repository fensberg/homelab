package config

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// Runner is the identity the cluster uses to register and retire its own
// ephemeral CI runners.
//
// It sits at the fleet plane rather than inside a site, for the same reason
// the object storage account does: one GitHub App serves the estate, and a
// runner is a site-level deployment of an estate-level identity. See the epoch
// record, "The self-hosted runner belongs in this epoch, not the next one".
//
// None of these three fields is a site secret, and none of them is powerful on
// its own - the App is scoped to organization self-hosted runners and nothing
// else, so the worst use of a stolen copy is registering or deleting runners.
// They live in the vault anyway, because the config template is meant to show
// the shape of the estate without naming it.
type Runner struct {
	AppID          string `json:"app_id"`
	InstallationID string `json:"installation_id"`

	// Base64 of the PEM, not the PEM. `op inject` substitutes values into a
	// JSON template, and a PEM's newlines would produce a file that does not
	// parse - so the encoding is a property of how the value travels, not a
	// security measure. ValidateRunner is what stops somebody discovering
	// that the hard way.
	PrivateKey string `json:"private_key"`
}

// ValidateRunner checks the runner credential as early as it can be checked.
//
// Every failure here is one that otherwise surfaces a long way from its cause:
// a name in place of an app id becomes a 401 from GitHub with no field named,
// a raw PEM becomes base64decode failing inside a plan, and base64 of the
// wrong thing becomes a Kubernetes secret full of bytes that are not a key,
// which fails when the controller first tries to mint a token. Each of those
// is expensive to diagnose and cheap to refuse.
func ValidateRunner(cfg *Config) error {
	r := cfg.Runner

	for _, f := range []struct {
		name, value string
	}{
		{"app_id", r.AppID},
		{"installation_id", r.InstallationID},
		{"private_key", r.PrivateKey},
	} {
		if strings.TrimSpace(f.value) == "" {
			return fmt.Errorf("runner.%s is empty. All three runner fields must be present: the App cannot authenticate without its id, its installation and its key", f.name)
		}
	}

	// Both ids are decimal numbers GitHub assigns. The likeliest wrong values
	// are the App's name and the settings URL the installation id is read
	// out of, and both are worth naming rather than passing along.
	for _, f := range []struct {
		name, value, where string
	}{
		{"app_id", r.AppID, "the App's settings page"},
		{"installation_id", r.InstallationID, "the end of the installation's settings URL"},
	} {
		if _, err := strconv.ParseUint(strings.TrimSpace(f.value), 10, 64); err != nil {
			return fmt.Errorf("runner.%s must be the number GitHub assigned, taken from %s - got %q", f.name, f.where, f.value)
		}
	}

	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(r.PrivateKey))
	if err != nil {
		return fmt.Errorf(`runner.private_key is not base64.

The downloaded .pem is stored base64-encoded on a single line, because this
config is a JSON template and a PEM's newlines would stop it parsing. Encode
it where the file already is rather than pasting it through a terminal:

    [Convert]::ToBase64String([IO.File]::ReadAllBytes("<the .pem>")) | Set-Clipboard`)
	}

	// Valid encoding is not evidence of valid content: base64 of anything at
	// all decodes cleanly, so check that what came back is the thing we asked
	// for rather than merely well-formed.
	if !strings.Contains(string(decoded), "PRIVATE KEY") {
		return fmt.Errorf("runner.private_key is valid base64 but does not decode to a PEM private key. Check that the encoded file was the .pem GitHub generated and not something else")
	}

	return nil
}
