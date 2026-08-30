package run

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Cmd runs an external command with inherited stdio, in dir. Go already
// distinguishes a nonzero exit from a launch failure via the returned error -
// unlike PowerShell, where $ErrorActionPreference does not apply to native
// exit codes and every native call needs its own check.
//
// name is never attacker-influenced: every call site in this module passes a
// hardcoded literal ("tofu", "age", "rclone" - verified by grepping every
// caller), and exec.Command never spawns a shell, so even a dynamic argument
// value cannot inject a second command. This is also an `internal/` package,
// so nothing outside this module can call it with a different name at all.
func Cmd(dir, name string, args ...string) error {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
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
// remember to unset afterwards. Same reasoning as Cmd above: name is always
// a hardcoded literal at every call site in this internal package.
func CmdEnv(dir string, extraEnv []string, name string, args ...string) error {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
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
// streamed to the terminal so failures are visible. Same reasoning as Cmd
// above: name is always a hardcoded literal at every call site in this
// internal package.
func CmdOutput(dir, name string, args ...string) (string, error) {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
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

    ./steward ignite -phase overlay -upgrade
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

// CmdStdin is Cmd with bytes fed to the child's stdin instead of the
// terminal's. It exists so that sensitive material can be handed to another
// process without going through a file: writing a secret to disk and deleting
// it afterwards assumes the delete happens, that nothing copied the file
// first, and that no backup, crash dump or snapshot caught it in between.
// A pipe makes all three assumptions unnecessary.
//
// Same reasoning as Cmd above: name is always a hardcoded literal at every
// call site in this internal package.
func CmdStdin(dir string, stdin []byte, name string, args ...string) error {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	c := exec.Command(name, args...)
	c.Dir = dir
	c.Stdin = bytes.NewReader(stdin)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("%s failed: %w", name, err)
	}
	return nil
}

// CmdOutputBytes is CmdOutput returning the raw bytes, so a caller holding
// something sensitive can wipe the buffer when it is done. A string could not
// be wiped - Go strings are immutable, and the bytes stay in the heap until
// the garbage collector happens to reuse them.
func CmdOutputBytes(dir, name string, args ...string) ([]byte, error) {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	c := exec.Command(name, args...)
	c.Dir = dir
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = os.Stderr
	err := c.Run()
	return out.Bytes(), err
}

// Wipe overwrites a buffer in place. Not a guarantee - the runtime may have
// copied it during a heap move - but it removes the copy this program is
// still holding, which is the one it can actually do something about.
func Wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// CmdOutputEnv is CmdOutput plus extra environment variables, for tools
// configured entirely through the environment so that no credential is ever
// written to a config file on disk.
func CmdOutputEnv(dir string, extraEnv []string, name string, args ...string) (string, error) {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	c := exec.Command(name, args...)
	c.Dir = dir
	c.Env = append(os.Environ(), extraEnv...)
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = os.Stderr
	err := c.Run()
	return strings.TrimSpace(out.String()), err
}

// CmdOutputQuiet is CmdOutput with stderr discarded, for probes whose failure
// is an ordinary answer rather than a problem worth showing the operator.
func CmdOutputQuiet(dir, name string, args ...string) (string, error) {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	c := exec.Command(name, args...)
	c.Dir = dir
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = io.Discard
	err := c.Run()
	return strings.TrimSpace(out.String()), err
}

// CmdBytes is the general form the others specialise: optional extra
// environment, optional stdin, and stdout captured as raw bytes rather than a
// trimmed string.
//
// Raw and untrimmed matters here. The restore pipeline moves age ciphertext
// and then OpenTofu state through it, and trimming whitespace off a binary
// stream corrupts it in a way that only shows up as a decryption failure with
// no obvious cause.
func CmdBytes(dir string, extraEnv []string, stdin []byte, name string, args ...string) ([]byte, error) {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	c := exec.Command(name, args...)
	c.Dir = dir
	if len(extraEnv) > 0 {
		c.Env = append(os.Environ(), extraEnv...)
	}
	if stdin != nil {
		c.Stdin = bytes.NewReader(stdin)
	}
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("%s failed: %w", name, err)
	}
	return out.Bytes(), nil
}
