package ansible

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A play that matched no hosts is a failure, not a clean dry run.
//
// WHAT THIS GUARDS. `task check-hypervisor` used to invoke ansible-playbook
// straight from the taskfile. With no inventory.yml - which is the NORMAL state
// of this workspace, because Sterilize removes it on every exit - Ansible
// printed two warnings, skipped the play, printed an empty recap, and exited 0:
//
//	[WARNING]: Unable to parse .../inventory.yml as an inventory source
//	[WARNING]: provided hosts list is empty, only localhost is available
//	PLAY RECAP *****
//	$ echo $?
//	0
//
// So a dry run that examined nothing was indistinguishable from one that found
// nothing to change, on the command whose whole job is telling you what a run
// would do before you touch a hypervisor (#322).
//
// Run against a stub ansible-playbook rather than the real one: the property is
// what this function does with the output, and a test needing a real playbook
// and a real inventory would not be hermetic.

// stubPlaybook stands in for ansible-playbook, printing whatever the test asks
// for and exiting 0 - which is the whole point, since the real one does too.
func stubPlaybook(t *testing.T, output string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/bash\ncat <<'OUTPUT'\n" + output + "\nOUTPUT\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "ansible-playbook"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestAPlaybookThatMatchedNoHostsIsRefused(t *testing.T) {
	dir := stubPlaybook(t, `[WARNING]: Unable to parse /x/inventory.yml as an inventory source
[WARNING]: provided hosts list is empty, only localhost is available

PLAY [Bootstrap Proxmox VE nodes] **********************************************
skipping: no hosts matched

PLAY RECAP *********************************************************************`)

	err := RunPlaybook(dir, nil)
	if err == nil {
		t.Fatal(`RunPlaybook returned nil for a play that matched no hosts.

ansible-playbook exits 0 there, so this is the only thing standing between
"I checked and found nothing to change" and "I checked nothing" - and they are
the same output otherwise.`)
	}
	if !strings.Contains(err.Error(), "checked nothing") {
		t.Errorf("the refusal does not say what went wrong: %v", err)
	}
	if !strings.Contains(err.Error(), "task check-hypervisor") {
		t.Errorf(`the refusal does not name a command the operator can paste: %v

A refusal naming a phase rather than a runnable command is a reference, and the
operator has to work out what to type from it.`, err)
	}
}

// And a play that did match hosts is not refused.
//
// The converse, because a check that always refused would be satisfied by
// breaking the verb entirely - and it would look exactly like this one working.
func TestAPlaybookThatMatchedHostsIsNotRefused(t *testing.T) {
	dir := stubPlaybook(t, `PLAY [Bootstrap Proxmox VE nodes] **********************************************

TASK [Gathering Facts] *********************************************************
ok: [redacted]

PLAY RECAP *********************************************************************
redacted                   : ok=41   changed=2    unreachable=0    failed=0`)

	if err := RunPlaybook(dir, nil); err != nil {
		t.Fatalf("RunPlaybook refused a run that did reach a host: %v", err)
	}
}

// The operator still sees the playbook's output.
//
// The refusal reads what the play printed, and the obvious way to do that is to
// capture it - which would leave somebody watching a forty-task playbook
// staring at nothing until it finished. It is tee'd instead, and this is the
// assertion that keeps it that way.
func TestThePlaybooksOutputStillReachesTheOperator(t *testing.T) {
	const marker = "TASK [Gathering Facts]"
	dir := stubPlaybook(t, marker+"\nPLAY RECAP ***\nredacted : ok=1 changed=0 unreachable=0 failed=0")

	stdout, read, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = read
	runErr := RunPlaybook(dir, nil)
	os.Stdout = saved
	_ = read.Close()

	var seen strings.Builder
	buf := make([]byte, 4096)
	for {
		n, readErr := stdout.Read(buf)
		if n > 0 {
			seen.Write(buf[:n])
		}
		if readErr != nil {
			break
		}
	}
	if runErr != nil {
		t.Fatalf("RunPlaybook: %v", runErr)
	}
	if !strings.Contains(seen.String(), marker) {
		t.Errorf(`the playbook's output did not reach stdout:

%q
Reading the output to decide whether the play matched anything must not cost
the operator the ability to watch it run.`, seen.String())
	}
}
