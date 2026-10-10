package phases

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"homelab/contractor/internal/capacity"
	"homelab/contractor/internal/run"
)

// recordCapacity reads what the site's workers hold and what work that
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
func recordCapacity(ctx *run.Context, dir string, taken time.Time) error {
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
	site, err := capacity.Read(nodes, pods)
	if err != nil {
		return err
	}
	raw, err := capacity.Marshal(site, taken.UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, capacity.File), append(raw, '\n'), 0o644); err != nil {
		return err
	}
	// The sum with no change in it: how near the edge the site is running,
	// said every time a record is taken.
	run.Info(capacity.Of(site, capacity.Change{}).String())
	run.Ok(fmt.Sprintf("the site's capacity is saved beside the record, in %s", capacity.File))
	return nil
}
