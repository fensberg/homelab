// Package onepassword wraps the `op` CLI calls the Render phase needs:
// confirming a session, and rendering the config template.
package onepassword

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ErrNoCLI is what every program says when there is no `op` to ask.
var ErrNoCLI = errors.New("1Password CLI ('op') not found on PATH")

// Available reports whether the op CLI is on PATH at all.
func Available() bool {
	_, err := exec.LookPath("op")
	return err == nil
}

// SignedIn reports whether `op` currently has a valid session.
func SignedIn() bool {
	return exec.Command("op", "whoami").Run() == nil
}

// SignIn runs an interactive `op signin`, inheriting the terminal so the
// operator can complete it.
func SignIn() error {
	c := exec.Command("op", "signin")
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// WhoamiEmail returns the signed-in account's email, for the confirmation
// message printed after signing in.
func WhoamiEmail() (string, error) {
	out, err := exec.Command("op", "whoami", "--format=json").Output()
	if err != nil {
		return "", err
	}
	var account struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(out, &account); err != nil {
		return "", err
	}
	return account.Email, nil
}

// Inject renders a template through `op inject`, substituting every op://
// reference. Its stdout (the output path, on success) is noise here.
func Inject(templatePath, outPath string) error {
	c := exec.Command("op", "inject", "-i", templatePath, "-o", outPath, "-f")
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("1Password inject (config): %w", err)
	}
	return nil
}

// Read resolves a single op:// reference, for values that must not go
// through the whole-template Inject pass - specifically, values that do not
// exist yet at Render time on a brand new site because a later phase is
// what creates them (the hypervisor phase's SSH credential, created by
// hypervisor-prep.yml, is read here by the compute phase rather than
// templated in config/management.tpl.json, so Render succeeding never
// depends on a phase that has not run yet).
func Read(ref string) (string, error) {
	out, err := exec.Command("op", "read", ref).Output()
	if err != nil {
		return "", fmt.Errorf("1Password read (%s): %w", ref, err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// Vaults names every vault the signed-in account can see.
//
// The lawyer's service account is scoped to the estate vault alone, so the
// answer is how it finds that vault without the repository naming it - and a
// second name in the answer means the token reaches more than the estate.
func Vaults() ([]string, error) {
	out, err := exec.Command("op", "vault", "list", "--format=json").Output()
	if err != nil {
		return nil, fmt.Errorf("1Password vault list: %w", err)
	}
	return vaultNames(out)
}

func vaultNames(out []byte) ([]string, error) {
	var vaults []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(out, &vaults); err != nil {
		return nil, fmt.Errorf("1Password vault list: %w", err)
	}
	names := make([]string, 0, len(vaults))
	for _, v := range vaults {
		names = append(names, v.Name)
	}
	return names, nil
}
