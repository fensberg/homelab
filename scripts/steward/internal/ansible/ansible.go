// Package ansible runs the hypervisor-prep playbook directly - natively, on
// Linux. The original script's whole reason to hop into WSL2 was that
// Ansible has no supported Windows control node; running from a Linux
// workstation removes that layer entirely rather than porting it.
package ansible

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// PreflightSSH proves each hypervisor is reachable by key before handing
// off to Ansible. Ansible reports an SSH failure as an UNREACHABLE task,
// which buries the actual cause - a missing host key, or no usable
// credential - under a play recap.
func PreflightSSH(hosts []string) error {
	for _, h := range hosts {
		c := exec.Command("ssh",
			"-o", "BatchMode=yes",
			"-o", "StrictHostKeyChecking=accept-new",
			"-o", "ConnectTimeout=10",
			"root@"+h, "true")
		var stderr bytes.Buffer
		c.Stderr = &stderr
		if err := c.Run(); err != nil {
			return fmt.Errorf(`cannot log in to root@%s without a password.

  ssh: %s

  Install this machine's key on the hypervisor, once:

      ssh-copy-id root@%s

  It asks for the Proxmox root password that one time. Every run after it
  is key-based and needs no password at all.`, h, strings.TrimSpace(stderr.String()), h)
		}
	}
	return nil
}

// RunPlaybook runs hypervisor-prep.yml in dir, with the given -e argument
// pairs. ansible.cfg is picked up by ambient discovery from dir, same as
// the `check-hypervisor` task already relies on - unlike the WSL path this
// replaces, a native checkout is not under a world-writable mount, so
// nothing has to force ANSIBLE_CONFIG explicitly.
func RunPlaybook(dir string, extraVars []string) error {
	args := []string{"-i", "inventory.yml", "hypervisor-prep.yml"}
	args = append(args, extraVars...)
	c := exec.Command("ansible-playbook", args...)
	c.Dir = dir
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("ansible-playbook: %w", err)
	}
	return nil
}
