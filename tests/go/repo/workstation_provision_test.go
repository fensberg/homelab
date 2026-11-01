package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"homelab/tests/parsed"
)

// The workstation's play, run for real against a hypervisor that is a
// stand-in: `qm` on PATH records what it was asked and answers as the test
// says, and the play runs on this machine with nothing to become.
//
// The build half fetches a cloud image and is not run here. What is run is
// every decision before it: a workstation that could not reach the estate is
// refused, one that exists is left alone, a teardown removes the machine, and
// a teardown that does not finish says which machine and how to free it.
//
// covers: ansible:workstation/provision.yml

type workstationRun struct {
	out    string
	err    error
	called []string
}

func runWorkstationPlay(t *testing.T, statusCode, destroyCode string, extra ...string) workstationRun {
	t.Helper()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Fatal("ansible-playbook is not on PATH; take delivery of it: scripts/take-delivery.sh ansible-core")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(dir, "calls")
	qm := "#!/usr/bin/env bash\necho \"$*\" >>" + calls + "\n" +
		"case \"$1\" in status) exit " + statusCode + " ;; destroy) exit " + destroyCode + " ;; esac\n"
	if err := os.WriteFile(filepath.Join(bin, "qm"), []byte(qm), 0o755); err != nil {
		t.Fatal(err)
	}
	inventory := filepath.Join(dir, "stand-in-hypervisor.yml")
	if err := os.WriteFile(inventory, []byte("all:\n  children:\n    hypervisor:\n      hosts:\n        stand-in:\n          ansible_connection: local\n          ansible_become: false\n          ansible_python_interpreter: \"{{ ansible_playbook_python }}\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := repository(t).Invoke(parsed.Run{Dir: "workstation", Env: []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"ANSIBLE_CONFIG=" + filepath.Join(dir, "none.cfg"),
		"ANSIBLE_LOCAL_TEMP=" + filepath.Join(dir, "tmp"),
		"ANSIBLE_REMOTE_TEMP=" + filepath.Join(dir, "tmp", "remote"),
		"ANSIBLE_NOCOLOR=1",
	}}, "ansible-playbook", append([]string{"-i", inventory, "provision.yml"}, extra...)...)
	out := append(stdout, stderr...)
	body, _ := os.ReadFile(calls)
	var called []string
	for _, l := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if l != "" {
			called = append(called, l)
		}
	}
	return workstationRun{string(out), err, called}
}

func TestTheWorkstationIsRefusedWhenItCouldNotReachTheEstate(t *testing.T) {
	heavy(t, "runs ansible-playbook")
	r := runWorkstationPlay(t, "2", "0")
	if r.err == nil || !strings.Contains(r.out, "overlay_auth_key is empty") {
		t.Fatalf("a workstation with no overlay key was not refused:\n%s", r.out)
	}
	if len(r.called) != 0 {
		t.Errorf("the hypervisor was asked %v before the refusal", r.called)
	}
}

func TestAWorkstationThatExistsIsLeftAlone(t *testing.T) {
	heavy(t, "runs ansible-playbook")
	r := runWorkstationPlay(t, "0", "0", "-e", "overlay_auth_key=stand-in")
	if r.err != nil {
		t.Fatalf("the play failed on a workstation that exists: %v\n%s", r.err, r.out)
	}
	if strings.Join(r.called, ";") != "status 9000" || !strings.Contains(r.out, "already exists - leaving it alone") {
		t.Errorf("a workstation that exists was not left alone; the hypervisor was asked %v:\n%s", r.called, r.out)
	}
}

func TestATeardownStopsAndDestroysTheWorkstation(t *testing.T) {
	heavy(t, "runs ansible-playbook")
	r := runWorkstationPlay(t, "0", "0", "-e", "state=absent")
	if r.err != nil {
		t.Fatalf("the teardown failed: %v\n%s", r.err, r.out)
	}
	if want := "status 9000;stop 9000;destroy 9000 --purge"; strings.Join(r.called, ";") != want {
		t.Errorf("the teardown asked %v, want %s", r.called, want)
	}
}

func TestATeardownThatDoesNotFinishSaysHowToFreeTheMachine(t *testing.T) {
	heavy(t, "runs ansible-playbook")
	r := runWorkstationPlay(t, "0", "1", "-e", "state=absent")
	if r.err == nil || !strings.Contains(r.out, "qm unlock 9000") {
		t.Fatalf("a teardown that did not finish did not say how to free the machine:\n%s", r.out)
	}
}
