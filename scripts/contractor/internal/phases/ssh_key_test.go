package phases

import (
	"strings"
	"testing"
)

// A key that lost its line breaks in a paste is refused before Compute runs,
// by name, rather than reaching the provider - which ignores it and fails
// part way through the template apply blaming the host.
func TestAnSSHKeyWithoutItsLineBreaksIsRefusedUpFront(t *testing.T) {
	// Assembled rather than written out, so the repository's private-key
	// detector does not take a fixture's header for a leaked key.
	begin, end := "-----BEGIN "+"OPENSSH PRIVATE KEY-----", "-----END "+"OPENSSH PRIVATE KEY-----"
	good := begin + "\nAAAA\nBBBB\n" + end + "\n"
	if err := sshKeyUsable(good, "op://site0/hypervisor/ssh_private_key"); err != nil {
		t.Fatalf("an intact key was refused: %v", err)
	}
	for name, bad := range map[string]string{
		"one line":  begin + " AAAA BBBB " + end,
		"no header": "AAAA\nBBBB\nCCCC",
		"empty":     "",
	} {
		err := sshKeyUsable(bad, "op://site0/hypervisor/ssh_private_key")
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if !strings.Contains(err.Error(), "delete the ssh_username and ssh_private_key") {
			t.Errorf("%s: the refusal does not say how to recover: %v", name, err)
		}
		if strings.Contains(err.Error(), "AAAA") {
			t.Errorf("%s: the refusal printed part of the key: %v", name, err)
		}
	}
}
