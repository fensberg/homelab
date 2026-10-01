package phases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"homelab/contractor/config"
	"homelab/contractor/internal/run"
)

// Before a site's machines are destroyed, every workload that has data takes a
// backup, and the teardown refuses to start if one cannot be taken (#588).
//
// WHY THIS EXISTS. Players were on the game server until an hour before the
// site was demolished for a rebuild. The rebuilt server restored the newest
// backup, taken forty-five minutes before they stopped, and everything after
// it was gone. Nothing failed: the world is backed up on an interval, and
// nothing took one when the site was about to be destroyed. A planned
// teardown is the one loss that is entirely avoidable.
//
// WHO KNOWS HOW. The workload does, and says so in a file in its own
// directory: modules/applications/<name>/before-teardown.json names the pod,
// the container, and the command that takes a backup now and exits zero when
// it has. This program knows no workload by name. It asks every one that has
// declared, and a workload that declares nothing has said it holds nothing a
// teardown would lose.
//
// Read from the repository rather than from the running pod's annotations,
// because what runs in production is a release, and a declaration that had to
// be released before it could be read would not protect the teardown that is
// about to happen.

// backupTimeout bounds one workload's backup. Long enough for a world of
// some size to be copied to object storage; short enough that a command which
// never returns is a refusal rather than a teardown nobody can start.
const backupTimeout = 10 * time.Minute

// errClusterUnreachable is the cluster's API not answering at all. Nothing is
// running that could be asked, and nothing here can change that; it is told
// apart from a backup that was asked for and failed.
var errClusterUnreachable = errors.New("the cluster's API does not answer")

// SaveWorkloads asks every workload that declared a backup to take one now.
//
// Three outcomes, kept apart on purpose:
//
//   - every declared workload that is running backed up: nil.
//   - a backup was asked for and did not succeed, or a declaration cannot be
//     read: an error, and the teardown does not start.
//   - the cluster's API does not answer: errClusterUnreachable. No workload is
//     running to be asked, so refusing would make a dead cluster impossible to
//     demolish. The caller says so plainly, before the operator confirms.
func SaveWorkloads(ctx *run.Context) error {
	declared, err := config.DeclaredBackups(ctx.RepoRoot)
	if err != nil {
		return err
	}
	if len(declared) == 0 {
		run.Info("no workload declares a backup to take before a teardown")
		return nil
	}

	kubeconfig, cleanup, err := writeKubeconfig(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", errClusterUnreachable, err)
	}
	defer cleanup()
	if _, err := kubectl(ctx, kubeconfig, "get", "--raw", "/readyz", "--request-timeout=20s"); err != nil {
		return errClusterUnreachable
	}

	for _, d := range declared {
		pods, err := runningPods(ctx, kubeconfig, d)
		if err != nil {
			return fmt.Errorf("%s: could not find out whether it is running, so could not back up %s: %w", d.Workload, d.What, err)
		}
		if len(pods) == 0 {
			run.Info(fmt.Sprintf("%s is not running here, so it has nothing a backup lacks", d.Workload))
			continue
		}
		for _, pod := range pods {
			run.Info(fmt.Sprintf("%s: backing up %s", d.Workload, d.What))
			if err := backUp(kubeconfig, d, pod); err != nil {
				return fmt.Errorf(`%s: the backup of %s did not succeed (%v).

Nothing has been destroyed. What the workload printed is not shown, because it
can name what it is backing up; to read it, run the same command:

    contractor kubeconfig -site %s -- kubectl exec -n %s %s -c %s -- %s`,
					d.Workload, d.What, err, ctx.Site, d.Namespace, pod, d.Container, strings.Join(d.Command, " "))
			}
			run.Ok(fmt.Sprintf("%s: %s is backed up", d.Workload, d.What))
		}
	}
	return nil
}

// runningPods is the names of the workload's pods that are running.
func runningPods(ctx *run.Context, kubeconfig string, d config.BeforeTeardown) ([]string, error) {
	out, err := kubectl(ctx, kubeconfig, "get", "pods", "-n", d.Namespace, "-l", d.Selector, "-o", "json", "--request-timeout=30s")
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("the list of pods is not JSON: %w", err)
	}
	var names []string
	for _, p := range list.Items {
		if p.Status.Phase == "Running" {
			names = append(names, p.Metadata.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// backUp runs the declared command in one pod and waits for it, up to
// backupTimeout. Its output is discarded: a workload's own words can name
// what it holds, and this runs in logs other people read.
func backUp(kubeconfig string, d config.BeforeTeardown, pod string) error {
	c, cancel := context.WithTimeout(context.Background(), backupTimeout)
	defer cancel()
	args := append([]string{"exec", "-n", d.Namespace, pod, "-c", d.Container, "--"}, d.Command...)
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	cmd := exec.CommandContext(c, "kubectl", args...)
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig)
	if err := cmd.Run(); err != nil {
		if c.Err() != nil {
			return fmt.Errorf("it had not finished after %s", backupTimeout)
		}
		return err
	}
	return nil
}
