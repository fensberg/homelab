package phases

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homelab/contractor/internal/fit"
	"homelab/contractor/internal/run"
)

// standIns puts a tofu that hands over a kubeconfig, and a kubectl that
// answers for the machines and the pods, ahead of the real ones.
func standIns(t *testing.T, nodes, pods string) {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("nodes.json", nodes, 0o600)
	write("pods.json", pods, 0o600)
	write("tofu", "#!/bin/sh\necho 'apiVersion: v1'\n", 0o755)
	write("kubectl", "#!/bin/sh\ncase \"$*\" in\n  *nodes*) cat "+filepath.Join(dir, "nodes.json")+" ;;\n  *pods*) cat "+
		filepath.Join(dir, "pods.json")+" ;;\n  *) exit 1 ;;\nesac\n", 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

const twoWorkers = `{"items":[
 {"metadata":{"name":"cp-0","labels":{"node-role.kubernetes.io/control-plane":""}},"status":{"allocatable":{"memory":"4Gi"}}},
 {"metadata":{"name":"wk-0","labels":{}},"status":{"allocatable":{"memory":"10Gi"}}},
 {"metadata":{"name":"wk-1","labels":{}},"status":{"allocatable":{"memory":"10Gi"}}}]}`

const oneRunningPod = `{"items":[{"metadata":{"name":"a"},"spec":{"nodeName":"wk-0","priorityClassName":"interactive",
 "containers":[{"resources":{"requests":{"memory":"3Gi"}}}]},"status":{"phase":"Running"}}]}`

// Taking a record keeps the site's standing beside it, read from the
// cluster: what a plan with no credential holds a change against.
func TestTakingARecordKeepsTheSitesStandingBesideIt(t *testing.T) {
	standIns(t, twoWorkers, oneRunningPod)
	ctx := &run.Context{Root: run.Root{Dir: t.TempDir()}}
	out := filepath.Join(t.TempDir(), "as-built")
	if err := recordStanding(ctx, out, time.Date(2026, 10, 9, 4, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(out, fit.File))
	if err != nil {
		t.Fatalf("no standing was kept beside the record: %v", err)
	}
	site, taken, err := fit.Unmarshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	const gib = int64(1) << 30
	if site.Workers != 20*gib || site.LargestWorker != 10*gib || site.MustRun != 3*gib {
		t.Errorf("two workers of ten with three reserved were kept as %+v", site)
	}
	if taken != "2026-10-09T04:00:00Z" {
		t.Errorf("it does not say when it was read: %q", taken)
	}
}

// A cluster that cannot be read leaves no standing behind. An old one, or
// one of nothing, would be a figure a change is let through against.
func TestAClusterThatCannotBeReadLeavesNoStanding(t *testing.T) {
	standIns(t, `{"items":[]}`, `{"items":[]}`)
	ctx := &run.Context{Root: run.Root{Dir: t.TempDir()}}
	out := filepath.Join(t.TempDir(), "as-built")
	err := recordStanding(ctx, out, time.Now())
	if err == nil || !strings.Contains(err.Error(), "none is a worker") {
		t.Fatalf("a cluster with no workers was recorded, or refused for another reason: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(out, fit.File)); statErr == nil {
		t.Error("a standing was written for a cluster that could not be read")
	}
}
