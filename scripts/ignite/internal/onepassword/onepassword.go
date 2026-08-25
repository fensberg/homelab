// Package onepassword wraps the `op` CLI calls the Render phase needs:
// confirming a session, and rendering the config template.
package onepassword

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

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
