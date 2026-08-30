package run

import (
	"os"
	"path/filepath"
	"testing"
)

// "Safe to run twice" is a property Sterilize's own doc comment claims and
// the failure path depends on: main.go can call Sterilize after
// EmergencyDestroy has already run, and an error on a second pass would turn
// a completed cleanup into a non-zero exit.

func TestRemoveIfExists_RemovesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rendered.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}

	if err := RemoveIfExists(path); err != nil {
		t.Fatalf("first removal: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the file survived removal: %v", err)
	}

	// The second pass is the point: a missing target is not an error, or
	// sterilizing twice would fail the run it was cleaning up after.
	if err := RemoveIfExists(path); err != nil {
		t.Errorf("second removal of an already-absent path returned %v; sterilize must be safe to run twice", err)
	}
}

func TestRemoveIfExists_NeverExistedIsNotAnError(t *testing.T) {
	if err := RemoveIfExists(filepath.Join(t.TempDir(), "never-written")); err != nil {
		t.Errorf("removing a path that never existed returned %v", err)
	}
}

// A directory is not something Sterilize's target list should ever contain,
// but os.Remove refuses a non-empty one - so this pins the behaviour rather
// than leaving it to be discovered during a failed run.
func TestRemoveIfExists_NonEmptyDirectoryReportsAnError(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "nested")
	if err := os.Mkdir(nested, 0o750); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "child"), []byte("x"), 0o600); err != nil {
		t.Fatalf("writing a child: %v", err)
	}

	if err := RemoveIfExists(nested); err == nil {
		t.Error("expected an error removing a non-empty directory, got nil")
	}
}

func TestFilepathBase(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/a/b/c.json", "c.json"},
		{"c.json", "c.json"},
		{"/c.json", "c.json"},
		{"", ""},
	} {
		if got := filepathBase(tc.in); got != tc.want {
			t.Errorf("filepathBase(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWipe(t *testing.T) {
	b := []byte("a state file full of private keys")
	Wipe(b)
	for i, c := range b {
		if c != 0 {
			t.Fatalf("byte %d is %q, not zero - the buffer was not wiped", i, c)
		}
	}
	// Length must survive, or callers that reuse the slice get a surprise.
	if len(b) != 33 {
		t.Errorf("Wipe changed the length to %d", len(b))
	}
}

func TestWipe_EmptyAndNil(t *testing.T) {
	Wipe(nil)
	Wipe([]byte{})
}

// The point of CmdStdin is that sensitive bytes reach another process without
// becoming a file. `cat` is the smallest possible proof that stdin arrives.
func TestCmdStdin_DeliversStdin(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")

	// sh -c writes whatever it reads to a file, so the test can check that
	// the bytes made the trip without inspecting the pipe itself.
	if err := CmdStdin(dir, []byte("state-bytes"), "sh", "-c", "cat > "+out); err != nil {
		t.Fatalf("CmdStdin: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading what the child wrote: %v", err)
	}
	if string(got) != "state-bytes" {
		t.Errorf("child received %q, want %q", got, "state-bytes")
	}
}

func TestCmdStdin_ReportsAFailingCommand(t *testing.T) {
	if err := CmdStdin(t.TempDir(), []byte("x"), "sh", "-c", "exit 3"); err == nil {
		t.Error("expected an error from a command that exited non-zero")
	}
}

func TestCmdOutputBytes(t *testing.T) {
	got, err := CmdOutputBytes(t.TempDir(), "sh", "-c", "printf 'hello'")
	if err != nil {
		t.Fatalf("CmdOutputBytes: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
	// Returned as bytes precisely so it can be wiped; prove that works on
	// the actual return value rather than on a synthetic slice.
	Wipe(got)
	if string(got) == "hello" {
		t.Error("the returned buffer could not be wiped")
	}
}
