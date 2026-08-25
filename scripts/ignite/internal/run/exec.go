package run

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Cmd runs an external command with inherited stdio, in dir. Go already
// distinguishes a nonzero exit from a launch failure via the returned error -
// unlike PowerShell, where $ErrorActionPreference does not apply to native
// exit codes and every native call needs its own check.
func Cmd(dir, name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Dir = dir
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("%s failed: %w", name, err)
	}
	return nil
}

// CmdEnv is Cmd plus extra environment variables, scoped to this one
// process rather than the parent's environment - so there is nothing to
// remember to unset afterwards.
func CmdEnv(dir string, extraEnv []string, name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Dir = dir
	c.Env = append(os.Environ(), extraEnv...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("%s failed: %w", name, err)
	}
	return nil
}

// CmdOutput runs a command and returns trimmed stdout, with stderr still
// streamed to the terminal so failures are visible.
func CmdOutput(dir, name string, args ...string) (string, error) {
	c := exec.Command(name, args...)
	c.Dir = dir
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = os.Stderr
	err := c.Run()
	return strings.TrimSpace(out.String()), err
}

// Tofu runs `tofu <args>` in the cluster directory.
func Tofu(ctx *Context, what string, args ...string) error {
	if err := Cmd(ctx.ClusterDir, "tofu", args...); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

// TofuApply runs a targeted (or full, if targets is empty) apply.
func TofuApply(ctx *Context, what string, targets ...string) error {
	args := []string{"apply", "-input=false", "-auto-approve"}
	for _, t := range targets {
		args = append(args, "-target="+t)
	}
	return Tofu(ctx, what, args...)
}

// TofuInit runs plain init by default, so the committed .terraform.lock.hcl
// decides provider versions and every machine gets the same ones. -upgrade
// re-resolves against the constraints, which is only correct right after a
// constraint changes.
func TofuInit(ctx *Context) error {
	args := []string{"init", "-input=false"}
	if ctx.Upgrade {
		args = append(args, "-upgrade")
	}
	err := Cmd(ctx.ClusterDir, "tofu", args...)
	if err == nil {
		return nil
	}
	if !ctx.Upgrade {
		return fmt.Errorf(`tofu init failed: %w

If it complained that a locked provider does not match its version
constraint, the committed lock file is behind management/cluster/versions.tf.
Re-resolve and commit the result:

    ./ignite -phase overlay -upgrade
    git add management/cluster/.terraform.lock.hcl && git commit`, err)
	}
	return fmt.Errorf("tofu init -upgrade failed: %w", err)
}

// TofuOutputRaw reads a single output value, failing loudly if it is
// missing or blank rather than letting an empty string travel further.
func TofuOutputRaw(ctx *Context, name string) (string, error) {
	out, err := CmdOutput(ctx.ClusterDir, "tofu", "output", "-raw", name)
	if err != nil || strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("could not read the %s output", name)
	}
	return out, nil
}

// RemoveIfExists deletes a path if present, logging what it removed. It is
// not an error for the path to already be gone - sterilizing is meant to be
// safe to run twice.
func RemoveIfExists(path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	Info("removed " + filepathBase(path))
	return nil
}

func filepathBase(path string) string {
	i := strings.LastIndexByte(path, '/')
	if i < 0 {
		return path
	}
	return path[i+1:]
}
