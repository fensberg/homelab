package run

import (
	"bufio"
	"bytes"
	"encoding/json"
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

// TofuApply runs a targeted (or full, if targets is empty) apply, and reports
// addresses and verbs rather than streaming what tofu would print.
//
// A plain apply writes the whole planned change to stdout, every non-sensitive
// attribute included. That is fine on a workstation and wrong in CI: a converge
// runs in a public repository's Actions log, so an attribute derived from a
// vault value is published to anyone. It is not hypothetical - a converge
// printed the site's real name inside a resource description, and rotating the
// name would only have published the new one.
//
// So this is the same rule `plan` already follows, applied to the verb that
// actually changes things. -json turns the stream into structured events whose
// hooks carry an address and an action and no attribute values; diagnostics are
// reported by summary, without the detail, because a detail can quote the value
// that caused it. The detail is still available by re-running on a workstation,
// which is where somebody debugging an apply already is.
func TofuApply(ctx *Context, what string, targets ...string) error {
	args := []string{"apply", "-input=false", "-auto-approve", "-json"}
	for _, t := range targets {
		args = append(args, "-target="+t)
	}
	return tofuJSON(ctx, what, args)
}

// TofuDestroy runs a destroy through the same JSON summary an apply uses.
//
// A destroy prints more than an apply, not less: every attribute of every
// resource being removed, read straight out of state. `talos_machine_secrets`
// alone carries the etcd, Kubernetes, aggregator and OS certificate
// authorities plus the cluster id, and the provider marks none of them
// sensitive - so a plain destroy publishes the estate's own PKI to whatever is
// reading the output.
//
// This was the one verb the -json discipline never reached, because it is the
// one that went through `run.Cmd` rather than either apply helper - so the
// rule "run.Tofu may only run init" looked satisfied while the largest leak in
// the program sat one function away from it.
func TofuDestroy(ctx *Context, what string) error {
	return tofuJSON(ctx, what, []string{"destroy", "-input=false", "-auto-approve", "-json"})
}

// StateSerial reports the serial number of the state this workspace is
// attached to.
//
// OpenTofu increments it on every write, so comparing it across a run answers
// "did anything actually change" as a fact rather than as an inference from
// which phase was running. Returns ok=false when it cannot be read at all -
// a locked state after an interrupted apply, most likely - which is a third
// answer and must not be collapsed into either of the other two.
func StateSerial(ctx *Context) (serial int64, ok bool) {
	out, err := CmdOutputQuiet(ctx.ClusterDir, "tofu", "state", "pull")
	if err != nil {
		return 0, false
	}
	var st struct {
		Serial *int64 `json:"serial"`
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil || st.Serial == nil {
		return 0, false
	}
	return *st.Serial, true
}

// TofuApplyArgs runs an apply whose flags the caller builds, through the same
// JSON summary TofuApply produces.
//
// It exists because `run.Tofu` streams whatever tofu prints, and an apply
// prints every non-sensitive attribute of everything it touches. The overlay
// phase used it and published the site's real name in a resource description
// to a public Actions log. There is no reason for two ways to run an apply
// when only one of them is safe to run where anyone can read it.
//
// The caller passes the whole argument list, including -json, because the one
// caller needs -replace and -target computed from state.
func TofuApplyArgs(ctx *Context, what string, args ...string) error {
	return tofuJSON(ctx, what, args)
}

// tofuEvent is the subset of tofu's -json stream this reports on. Every other
// field is ignored rather than filtered, so a new field in a future version
// cannot start leaking by default.
type tofuEvent struct {
	Level   string `json:"@level"`
	Type    string `json:"type"`
	Message string `json:"@message"`
	Hook    struct {
		Resource struct {
			Addr string `json:"addr"`
		} `json:"resource"`
		Action         string  `json:"action"`
		ElapsedSeconds float64 `json:"elapsed_seconds"`
	} `json:"hook"`
	Diagnostic struct {
		Severity string `json:"severity"`
		Summary  string `json:"summary"`
		Detail   string `json:"detail"`
	} `json:"diagnostic"`
	Changes struct {
		Add    int `json:"add"`
		Change int `json:"change"`
		Remove int `json:"remove"`
	} `json:"changes"`
}

// publicLog reports whether this run's output is world-readable. A converge in
// Actions lands in a public repository's log; the same command on a workstation
// does not, and the two want different amounts of detail.
func publicLog() bool {
	return os.Getenv("GITHUB_ACTIONS") == "true" || os.Getenv("CI") == "true"
}

// summariseApply reduces tofu's -json stream to addresses and verbs. It is
// separated from the process handling so it can be tested against a recorded
// stream: the property that matters is that no attribute value survives, and
// that is a property of this function alone.
func summariseApply(r io.Reader, emit func(string)) (lines []string, failed []string) {
	if emit == nil {
		emit = func(string) {}
	}
	say := func(line string) {
		lines = append(lines, line)
		emit(line)
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var ev tofuEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			// Not JSON, so not something whose shape is known. Dropping it is
			// the safe default: an unrecognised line could carry anything.
			continue
		}
		switch ev.Type {
		// tofu's own "Still destroying... [30s elapsed]". Dropping it left a
		// long destroy silent from the first line to the last, on the one
		// operation in this program that cannot be safely interrupted - and an
		// operator with no output cannot tell a ten-minute data source read
		// from a hang. It carries an address and a duration and no attribute
		// values, so it is as safe to print as apply_start already is.
		case "apply_progress":
			if addr := ev.Hook.Resource.Addr; addr != "" {
				say(fmt.Sprintf("%-9s %s (%ds elapsed)",
					"still", addr, int(ev.Hook.ElapsedSeconds)))
			}
		case "apply_start", "apply_complete", "apply_errored":
			if addr := ev.Hook.Resource.Addr; addr != "" {
				say(fmt.Sprintf("%-9s %s", ev.Hook.Action, addr))
			}
		case "change_summary":
			say(fmt.Sprintf("%d added, %d changed, %d destroyed",
				ev.Changes.Add, ev.Changes.Change, ev.Changes.Remove))
		case "diagnostic":
			if ev.Diagnostic.Severity == "error" {
				msg := ev.Diagnostic.Summary
				// The detail is withheld only where the output is published.
				// Suppressing it on a workstation costs the person debugging
				// the one thing that would tell them what went wrong, and buys
				// nothing - which is exactly what happened the first time this
				// fired: "Failed to create key", with the reason stripped, on a
				// terminal nobody but the operator could see.
				if !publicLog() && ev.Diagnostic.Detail != "" {
					msg += ": " + ev.Diagnostic.Detail
				}
				failed = append(failed, msg)
			}
		}
	}
	return lines, failed
}

func tofuJSON(ctx *Context, what string, args []string) error {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	c := exec.Command("tofu", args...)
	c.Dir = ctx.ClusterDir
	stdout, err := c.StdoutPipe()
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	// tofu writes its diagnostics into the JSON stream under -json; anything
	// reaching stderr is the binary itself failing, which carries no attributes.
	c.Stderr = os.Stderr
	if err := c.Start(); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}

	// Printed as the stream is read, not collected and printed afterwards.
	//
	// summariseApply reads to EOF, which for a single long-running invocation
	// is when tofu exits - so buffering meant a destroy of a whole estate
	// printed its first line and its last line at the same moment, ten minutes
	// in. The apply path hid it because ignition applies in short targeted
	// steps; the destroy is one call that runs for as long as the estate takes
	// to remove.
	_, failed := summariseApply(stdout, Info)
	waitErr := c.Wait()
	if len(failed) > 0 {
		return fmt.Errorf("%s: %s%s",
			what, strings.Join(failed, "; "), detailNote())
	}
	if waitErr != nil {
		return fmt.Errorf("%s failed: %w", what, waitErr)
	}
	return nil
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

    ./contractor break-ground -phase overlay -upgrade
    git add management/cluster/.terraform.lock.hcl && git commit`, err)
	}
	return fmt.Errorf("tofu init -upgrade failed: %w", err)
}

// TofuOutputRaw reads a single output value, failing loudly if it is
// missing or blank rather than letting an empty string travel further.
func TofuOutputRaw(ctx *Context, name string) (string, error) {
	// -no-color, because this output is a value rather than something a
	// person reads. tofu writes "Warning: No outputs found" to stdout and
	// exits zero, so a workspace with no state hands back a coloured
	// diagnostic that passes both checks below and gets used as the value.
	// Stripping the colour does not fix that on its own - the caller still
	// has to know what it asked for - but it stops escape sequences reaching
	// a file, which is what turns a clear failure into "control characters
	// are not allowed" somewhere else entirely.
	out, err := CmdOutput(ctx.ClusterDir, "tofu", "output", "-no-color", "-raw", name)
	if err != nil || strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("could not read the %s output", name)
	}
	// tofu's diagnostics are boxed with these, and they never appear in a
	// value. Exit code zero plus a warning on stdout is the case this exists
	// for, and it is not hypothetical: it produced a kubeconfig that was a
	// warning message.
	if strings.ContainsAny(out, "╷╵│") {
		return "", fmt.Errorf("reading the %s output returned a diagnostic rather than a value - the state is probably not reachable from this workspace", name)
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

// detailNote explains a missing detail, and only when one is actually missing.
func detailNote() string {
	if publicLog() {
		return "\n\nSummaries only - tofu's detail can quote the value that caused the error, and this runs in a public log. Re-run on a workstation for the full diagnostic"
	}
	return ""
}
