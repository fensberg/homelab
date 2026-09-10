package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The entrypoint assembles the server's arguments, and it is run rather than
// read.
//
// It exists because the server has no neutral value for a world modifier and no
// way to express "off" for a flag, so an unset dial has to be an ABSENT
// argument - and Kubernetes expands $(VAR) into exactly one argument, so a
// manifest cannot express that. The whole point of the script is which
// arguments do not appear, which is precisely what reading it cannot check.
//
// Driven through a stub binary that prints what it was handed. That is what the
// VALHEIM_SERVER_BINARY seam is for and the only thing it is for.
func runEntrypoint(t *testing.T, env map[string]string) (string, int) {
	t.Helper()

	dir := t.TempDir()
	stub := filepath.Join(dir, "stub")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nfor a in \"$@\"; do printf '[%s]' \"$a\"; done\n"), 0o700); err != nil {
		t.Fatalf("writing the stub: %v", err)
	}

	// The filename is spelled out inside the call, on one line, with no
	// closing parenthesis before it - filepath.Join would put one there.
	//
	// That is not fussiness. The coverage checker looks for an exec of the file
	// BY NAME, line by line, and stops at the first bracket. Hide the name
	// behind a variable or a helper call and the script counts as executed by
	// nothing while this test runs it every time - the exact false signal that
	// check exists to refuse.
	root := repoRoot(t)
	cmd := exec.Command("/bin/sh", root+"/modules/applications/valheim/image/entrypoint.sh")
	// Stated rather than inherited: a test that reads the machine's
	// environment is a test that behaves differently on somebody else's.
	cmd.Env = []string{"PATH=/usr/bin:/bin", "VALHEIM_SERVER_BINARY=" + stub}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	out, err := cmd.CombinedOutput()
	code := 0
	var ee *exec.ExitError
	if err != nil {
		if !asExitError(err, &ee) {
			t.Fatalf("running the entrypoint: %v", err)
		}
		code = ee.ExitCode()
	}
	return string(out), code
}

func asExitError(err error, target **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if ok {
		*target = ee
	}
	return ok
}

func required() map[string]string {
	return map[string]string{
		"VALHEIM_SERVER_NAME": "a-server",
		"VALHEIM_WORLD_NAME":  "a-world",
		"VALHEIM_PASSWORD":    "a-password",
	}
}

// An unset dial produces no argument at all.
//
// This is the case the whole script exists for. Every value the server accepts
// for a modifier CHANGES the game - there is no "normal" - so a dial left alone
// has to be absent rather than defaulted, and a manifest cannot express that.
func TestAnUnsetDialProducesNoArgument(t *testing.T) {
	env := required()
	env["VALHEIM_PRESET"] = "normal"
	env["VALHEIM_MODIFIER_COMBAT"] = ""
	env["VALHEIM_MODIFIER_RAIDS"] = ""

	out, code := runEntrypoint(t, env)
	if code != 0 {
		t.Fatalf("exited %d: %s", code, out)
	}
	if strings.Contains(out, "-modifier") {
		t.Errorf("an empty dial reached the server as an argument:\n%s\n\n"+
			"Empty has to mean absent. Passed through, it becomes a value that changes "+
			"the game - the server has no token meaning \"leave this alone\".", out)
	}
	if !strings.Contains(out, "[-preset][normal]") {
		t.Errorf("the preset did not reach the server:\n%s", out)
	}
}

// A set dial appears exactly once, as a key and a value.
func TestASetDialIsPassedAsAKeyAndValue(t *testing.T) {
	env := required()
	env["VALHEIM_MODIFIER_RAIDS"] = "none"
	env["VALHEIM_MODIFIER_PORTALS"] = "casual"

	out, code := runEntrypoint(t, env)
	if code != 0 {
		t.Fatalf("exited %d: %s", code, out)
	}
	for _, want := range []string{"[-modifier][raids][none]", "[-modifier][portals][casual]"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in:\n%s", want, out)
		}
	}
}

// A flag is only ever turned on, because the server cannot turn one off.
//
// "false" has to mean absent rather than `-setkey nomap`. Passing that flag
// disables the map for everybody, so getting this backwards is not a subtle
// failure - it is a different game.
func TestAFlagIsOnlyPassedWhenItIsTrue(t *testing.T) {
	env := required()
	env["VALHEIM_SETKEY_NOMAP"] = "false"
	env["VALHEIM_SETKEY_PASSIVEMOBS"] = "true"

	out, code := runEntrypoint(t, env)
	if code != 0 {
		t.Fatalf("exited %d: %s", code, out)
	}
	if strings.Contains(out, "nomap") {
		t.Errorf("a flag set to \"false\" was passed anyway:\n%s\n\n"+
			"The server has no argument that turns one off, so passing it turns it ON. "+
			"Here that would disable the map for every player.", out)
	}
	if !strings.Contains(out, "[-setkey][passivemobs]") {
		t.Errorf("a flag set to \"true\" was not passed:\n%s", out)
	}
}

// Crossplay is a flag with no value, and it is what removes the port forward.
func TestCrossplayIsPassedAsABareFlag(t *testing.T) {
	out, code := runEntrypoint(t, required())
	if code != 0 {
		t.Fatalf("exited %d: %s", code, out)
	}
	if !strings.Contains(out, "[-crossplay]") {
		t.Errorf("crossplay was not enabled by default:\n%s\n\n"+
			"Without it the server uses the Steam backend, which needs an inbound port "+
			"forward - the design this estate deliberately does not have.", out)
	}
	if strings.Contains(out, "[-crossplay][true]") {
		t.Errorf("crossplay was given a value:\n%s\n\nIt takes none.", out)
	}
}

// A missing value stops the server, and says which one.
//
// The stopping is not what the explicit check buys. `set -u` already refuses an
// unset variable, so removing the check still fails - just as "parameter not
// set" with no field named, from a script the operator has never read, at the
// moment a server will not start.
//
// So this asserts the message. A check whose only contribution is a better
// error has to be tested on the error, or the test passes with the check
// deleted and reports coverage that is not there.
func TestAMissingValueRefusesToStartAndNamesTheField(t *testing.T) {
	env := required()
	delete(env, "VALHEIM_WORLD_NAME")

	out, code := runEntrypoint(t, env)
	if code == 0 {
		t.Fatalf("started with no world name:\n%s\n\n"+
			"An empty world name does not fail - it creates a world called \"\", which "+
			"is a real save file nobody meant to make.", out)
	}
	if !strings.Contains(out, "VALHEIM_WORLD_NAME") {
		t.Errorf("refused to start without naming the field that was missing:\n%s\n\n"+
			"The operator sees this at the moment a server will not come up. "+
			"\"parameter not set\" sends them to read a script; naming the variable "+
			"sends them to the Secret.", out)
	}
}
