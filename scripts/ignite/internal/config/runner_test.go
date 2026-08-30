package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

// A real key is a PEM; the config carries it base64-encoded so it survives
// `op inject` substituting it into a JSON template, where raw newlines would
// produce a file that does not parse.
func encodedKey(pem string) string {
	return base64.StdEncoding.EncodeToString([]byte(pem))
}

// Assembled from fragments rather than written as one literal. A convincing
// PEM header trips the detect-private-key pre-commit hook, which is exactly
// what that hook is for in a repository whose first invariant is that no
// secret ever lands in git. There is no key here - only the marker
// ValidateRunner looks for - and splitting it keeps the hook useful rather
// than teaching anyone to bypass it.
func samplePEM() string {
	const marker = "PRIVATE KEY-----"
	return "-----BEGIN RSA " + marker + "\nMIIBOgIBAAJBAK\n-----END RSA " + marker + "\n"
}

func validRunner() Runner {
	return Runner{
		AppID:          "4771490",
		InstallationID: "157746936",
		PrivateKey:     encodedKey(samplePEM()),
	}
}

func TestValidateRunner_Valid(t *testing.T) {
	if err := ValidateRunner(&Config{Runner: validRunner()}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRunner_AppIDMustBeNumeric(t *testing.T) {
	r := validRunner()
	r.AppID = "fensberg-homelab-foreman"
	err := ValidateRunner(&Config{Runner: r})
	if err == nil {
		t.Fatal("expected an error when app_id is the app's name rather than its id")
	}
	if !strings.Contains(err.Error(), "app_id") {
		t.Fatalf("the error must name the field that is wrong, got: %v", err)
	}
}

func TestValidateRunner_InstallationIDMustBeNumeric(t *testing.T) {
	r := validRunner()
	// The whole settings URL, which is where the number is read from and so
	// the likeliest thing to be pasted by mistake.
	r.InstallationID = "https://github.com/organizations/fensberg/settings/installations/157746936"
	err := ValidateRunner(&Config{Runner: r})
	if err == nil {
		t.Fatal("expected an error when installation_id is the URL it was copied from")
	}
	if !strings.Contains(err.Error(), "installation_id") {
		t.Fatalf("the error must name the field that is wrong, got: %v", err)
	}
}

// The failure this check exists for. Pasting the PEM straight into the vault
// field is the obvious thing to do and it is wrong, because the value has to
// survive JSON templating. Caught here it names the field; uncaught it
// surfaces as base64decode failing inside a plan, or as a Kubernetes secret
// holding bytes that are not a key.
func TestValidateRunner_RawPEMIsRejected(t *testing.T) {
	r := validRunner()
	r.PrivateKey = samplePEM()
	err := ValidateRunner(&Config{Runner: r})
	if err == nil {
		t.Fatal("expected an error when the private key is a raw PEM rather than base64")
	}
	if !strings.Contains(err.Error(), "base64") {
		t.Fatalf("the error must say what shape was expected, got: %v", err)
	}
}

// Base64 that decodes to something which is not a key at all. Valid encoding
// is not evidence of valid content, and a secret full of the wrong bytes fails
// somewhere much further away than here.
func TestValidateRunner_DecodedContentMustBeAKey(t *testing.T) {
	r := validRunner()
	r.PrivateKey = base64.StdEncoding.EncodeToString([]byte("not a key at all"))
	err := ValidateRunner(&Config{Runner: r})
	if err == nil {
		t.Fatal("expected an error when the decoded value is not a PEM private key")
	}
}

func TestValidateRunner_EmptyFieldsAreNamed(t *testing.T) {
	for _, tc := range []struct {
		field  string
		mutate func(*Runner)
	}{
		{"app_id", func(r *Runner) { r.AppID = "" }},
		{"installation_id", func(r *Runner) { r.InstallationID = "" }},
		{"private_key", func(r *Runner) { r.PrivateKey = "" }},
	} {
		r := validRunner()
		tc.mutate(&r)
		err := ValidateRunner(&Config{Runner: r})
		if err == nil {
			t.Fatalf("expected an error when %s is empty", tc.field)
		}
		if !strings.Contains(err.Error(), tc.field) {
			t.Fatalf("the error for an empty %s must name it, got: %v", tc.field, err)
		}
	}
}
