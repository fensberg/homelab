package capacity

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// A trial cluster: Kubernetes' own programs, at the version the site runs,
// with no machine under them, started and stopped by the tool Kubernetes' own
// project keeps for that. The engineer tries a change in one; a test that
// needs a real API server and nothing behind it runs in one.

// TrialStarter is what starts and stops a trial cluster, and TrialStore the
// store it keeps everything in.
const (
	TrialStarter = "kwokctl"
	TrialStore   = "etcd"
)

// Trial is a running trial cluster: where it works, the credential that
// reaches it, and what a program run against it needs in its environment.
type Trial struct {
	Dir, Kubeconfig string
	Env             []string
	stop            func()
}

// Stop stops the cluster and removes everything it kept. If it will not stop
// there is nothing to do about it, and its directory goes in any case.
func (t Trial) Stop() { t.stop() }

// StartTrial starts a trial cluster of the store and the given programs,
// each the one already delivered where there is one: the starter would
// otherwise fetch its own, unpinned, from the network.
func StartTrial(programs ...string) (Trial, error) {
	dir, err := os.MkdirTemp("", "trial-")
	if err != nil {
		return Trial{}, err
	}
	name := strings.ToLower(filepath.Base(dir))
	t := Trial{Dir: dir, Kubeconfig: filepath.Join(dir, "kubeconfig")}
	t.Env = []string{"KWOK_WORKDIR=" + filepath.Join(dir, "work"), "KUBECONFIG=" + t.Kubeconfig}

	components := append([]string{TrialStore}, programs...)
	start := []string{"create", "cluster", "--name", name, "--runtime", "binary",
		"--components", strings.Join(components, ","), "--kubeconfig", t.Kubeconfig}
	for _, program := range components {
		if path, err := exec.LookPath(program); err == nil {
			start = append(start, "--"+program+"-binary", path)
		}
	}
	finished, err := oneStartAtATime()
	if err != nil {
		_ = os.RemoveAll(dir)
		return Trial{}, fmt.Errorf("the trial cluster could not wait its turn to start: %w", err)
	}
	err = t.run(start...)
	finished()
	if err != nil {
		_ = os.RemoveAll(dir)
		return Trial{}, fmt.Errorf("the trial cluster could not be started: %w", err)
	}
	t.stop = func() {
		_ = t.run("delete", "cluster", "--name", name)
		_ = os.RemoveAll(dir)
	}
	return t, nil
}

// oneStartAtATime holds back every other start on this machine until the
// function it returns is called. The starter counts down from one port for
// each program and takes the first that is free at that moment, and nothing
// holds it until the program is up, so two started together can pick the same
// one and each then reach the other's API server. The lock is a file because
// the starts to keep apart are in different processes: a proof of the ledger
// runs its guard in a process of its own.
func oneStartAtATime() (func(), error) {
	lock, err := os.OpenFile(filepath.Join(os.TempDir(), fmt.Sprintf("trials-starting-%d.lock", os.Getuid())), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		_ = lock.Close()
		return nil, err
	}
	return func() { _ = lock.Close() }, nil
}

func (t Trial) run(args ...string) error {
	// proved by TestEveryProgramRunIsNamedByAConstantOrIsTheOperators
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	cmd := exec.Command(TrialStarter, args...)
	cmd.Env = append(os.Environ(), t.Env...)
	var complaints bytes.Buffer
	cmd.Stderr = &complaints
	if err := cmd.Run(); err != nil {
		lines := strings.Split(strings.TrimSpace(complaints.String()), "\n")
		return fmt.Errorf("%s %s: %w: %s", TrialStarter, strings.Join(args, " "), err, lines[len(lines)-1])
	}
	return nil
}
