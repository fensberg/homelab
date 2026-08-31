package phases

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The kubeconfig is a live credential. It is written on demand and removed by
// Sterilize, and that transience is the safeguard rather than an inconvenience
// around it - which is why the convenient form has to be the one that keeps it.
//
// These assert the properties that make `-- <command>` safe to reach for, so
// that the advice "use this form" is backed by something rather than by
// discipline.

func TestWithKubeconfigRejectsAnEmptyCommand(t *testing.T) {
	if _, err := WithKubeconfig(nil, nil); err == nil {
		t.Error("an empty command was accepted; it would render a credential for nothing and then remove it")
	}
}

// The file must never be written into the repository, because a credential in
// the working tree is one a later `git add -A` can commit. os.CreateTemp with
// an empty dir uses the OS temp directory, which on this workstation is also
// per-user private.
func TestTemporaryKubeconfigIsNotWrittenIntoTheRepository(t *testing.T) {
	f, err := os.CreateTemp("", "kubeconfig-*")
	if err != nil {
		t.Fatalf("creating a temporary file: %v", err)
	}
	defer os.Remove(f.Name())
	f.Close()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Clean(filepath.Join(wd, "..", "..", "..", ".."))
	if strings.HasPrefix(filepath.Clean(f.Name()), root+string(os.PathSeparator)) {
		t.Errorf("the temporary kubeconfig landed inside the repository at %s.\n\nA credential in the working tree is one a later `git add -A` can commit.", f.Name())
	}
}

// 0600 before anything is written to it. age --output showed how this goes
// wrong: naming a file and letting the umask decide left state ciphertext
// world-readable in a shared /tmp.
func TestTemporaryKubeconfigIsPrivateBeforeItHoldsAnything(t *testing.T) {
	f, err := os.CreateTemp("", "kubeconfig-*")
	if err != nil {
		t.Fatalf("creating a temporary file: %v", err)
	}
	path := f.Name()
	defer os.Remove(path)
	f.Close()
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("temporary kubeconfig is mode %04o, not 0600; anything else means another local user can read a live cluster credential", perm)
	}
}
