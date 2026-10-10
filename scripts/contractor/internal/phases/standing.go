package phases

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"homelab/contractor/internal/fit"
	"homelab/contractor/internal/run"
)

// recordStanding reads what the site's workers hold and what work that
// cannot wait reserves on them, and keeps it beside the record.
//
// Here, at the end of taking a record, because this is after the cluster
// has settled: a converge has applied its change and waited for it, and a
// reading taken now is the site as that change left it. A pull request's
// plan holds no credential and cannot ask the cluster anything, so this is
// what it holds a change against.
//
// Asked of the cluster itself, the way the health phase asks it. Only the
// cluster knows what is running. ctx is the cluster root's, whose output the
// kubeconfig is.
func recordStanding(ctx *run.Context, dir string, taken time.Time) error {
	kubeconfig, cleanup, err := writeKubeconfig(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	nodes, err := kubectl(ctx, kubeconfig, "get", "nodes", "-o", "json")
	if err != nil {
		return err
	}
	pods, err := kubectl(ctx, kubeconfig, "get", "pods", "--all-namespaces", "-o", "json")
	if err != nil {
		return err
	}
	site, err := fit.Standing(nodes, pods)
	if err != nil {
		return err
	}
	raw, err := fit.Marshal(site, taken.UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, fit.File), append(raw, '\n'), 0o644); err != nil {
		return err
	}
	// The sum with no change in it: how near the edge the site is running,
	// said every time a record is taken.
	run.Info(fit.Of(site, fit.Change{}).String())
	run.Ok(fmt.Sprintf("the site's standing is saved beside the record, in %s", fit.File))
	return nil
}
