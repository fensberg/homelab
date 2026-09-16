// Package ansible runs the hypervisor-prep playbook directly - natively, on
// Linux. The original script's whole reason to hop into WSL2 was that
// Ansible has no supported Windows control node; running from a Linux
// workstation removes that layer entirely rather than porting it.
package ansible

import (
	"bytes"
	"fmt"
	"io"
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

// A play that matched nothing. Ansible prints this and exits 0.
const noHostsMatched = "no hosts matched"

// RunPlaybook runs hypervisor-prep.yml in dir, with the given -e argument
// pairs. ansible.cfg is picked up by ambient discovery from dir, same as
// the `check-hypervisor` task already relies on - unlike the WSL path this
// replaces, a native checkout is not under a world-writable mount, so
// nothing has to force ANSIBLE_CONFIG explicitly.
//
// A RUN THAT MATCHED NO HOSTS IS A FAILURE, and making it one is most of why
// this function exists rather than a bare exec.
//
// With no inventory.yml - which is the NORMAL state of this workspace, because
// Sterilize wipes it on every exit - ansible-playbook prints two warnings,
// skips the play, prints an empty recap, and exits 0:
//
//	[WARNING]: Unable to parse .../inventory.yml as an inventory source
//	[WARNING]: provided hosts list is empty, only localhost is available
//	PLAY RECAP *****
//	$ echo $?
//	0
//
// A dry run that examined nothing is then indistinguishable from a dry run
// that found nothing to change, on the command whose entire job is to tell you
// what a run would do before you do it (#322). That is the failure this estate
// keeps having to repair - "nothing is wrong" and "nothing was checked"
// producing the same green - and it was sitting on the command an operator uses
// specifically to gain confidence before touching a hypervisor.
//
// Detected from the output rather than by stat-ing the inventory first, because
// the inventory existing is not the property. A file that parses but resolves
// to an empty group, a --limit matching nothing, a host list rendered for
// another site: all of them produce the same empty recap and the same exit 0,
// and only reading what the play actually did catches them all.
func RunPlaybook(dir string, extraVars []string) error {
	args := []string{"-i", "inventory.yml", "hypervisor-prep.yml"}
	args = append(args, extraVars...)
	c := exec.Command("ansible-playbook", args...)
	c.Dir = dir
	c.Stdin = os.Stdin

	// Tee'd rather than captured: the operator watches a playbook run, so
	// swallowing its output to inspect it afterwards would be a worse tool.
	var seen bytes.Buffer
	c.Stdout = io.MultiWriter(os.Stdout, &seen)
	c.Stderr = io.MultiWriter(os.Stderr, &seen)

	runErr := c.Run()
	if strings.Contains(strings.ToLower(seen.String()), noHostsMatched) {
		return fmt.Errorf(`the playbook matched no hosts, so it checked nothing and changed nothing.

ansible-playbook reports this and exits 0, which is indistinguishable from a
run that found nothing to do - so this refuses instead.

The inventory is written by the Render phase and removed by Sterilize on every
exit, deliberately: a rendered credential's short life is the safeguard. So an
empty workspace is the normal state, not a broken one. Run the verb through the
contractor, which renders first:

    task check-hypervisor SITE=<site>`)
	}
	if runErr != nil {
		return fmt.Errorf("ansible-playbook: %w", runErr)
	}
	return nil
}
