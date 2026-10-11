package phases

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homelab/contractor/internal/run"
	"homelab/details/capacity"
)

// standIns puts a tofu that hands over a kubeconfig, and a kubectl that
// answers for the machines and the pods and has nothing else to list, ahead
// of the real ones.
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
		filepath.Join(dir, "pods.json")+" ;;\n  *) echo '"+capacity.Nothing+"' ;;\nesac\n", 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

const twoWorkers = `{"items":[
 {"metadata":{"name":"cp-0","labels":{"node-role.kubernetes.io/control-plane":""}},"status":{"allocatable":{"memory":"4Gi"}}},
 {"metadata":{"name":"wk-0","labels":{}},"status":{"allocatable":{"memory":"10Gi"}}},
 {"metadata":{"name":"wk-1","labels":{}},"status":{"allocatable":{"memory":"10Gi"}}}]}`

const oneRunningPod = `{"items":[{"metadata":{"name":"a"},"spec":{"nodeName":"wk-0","priorityClassName":"interactive",
 "containers":[{"resources":{"requests":{"memory":"3Gi"}}}]},"status":{"phase":"Running"}}]}`

// Taking a record keeps the site's capacity beside it, read from the
// cluster: what a plan with no credential holds a change against.
func TestTakingARecordKeepsTheSitesCapacityBesideIt(t *testing.T) {
	standIns(t, twoWorkers, oneRunningPod)
	ctx := &run.Context{Root: run.Root{Dir: t.TempDir()}, RepoRoot: t.TempDir(), Site: "sitex"}
	out := filepath.Join(t.TempDir(), "as-built")
	if err := recordCapacity(ctx, out, time.Date(2026, 10, 8, 4, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(out, capacity.File))
	if err != nil {
		t.Fatalf("no capacity was kept beside the record: %v", err)
	}
	site, err := capacity.Unmarshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(site.Workers(), " "); got != "wk-0 wk-1" || len(site.Of("Pod")) != 1 {
		t.Errorf("two workers and one pod were kept as workers %q and %d pod(s)", got, len(site.Of("Pod")))
	}
	if !strings.Contains(site.String(), "wk-0 holds 10240 MiB for pods and 3072 is reserved on it") {
		t.Errorf("the site says how it stands as %q", site.String())
	}
	if site.Taken != "2026-10-08T04:00:00Z" {
		t.Errorf("it does not say when it was read: %q", site.Taken)
	}
}

// A cluster that cannot be read leaves no capacity file behind. An old one, or
// one of nothing, would be a figure a change is let through against.
func TestAClusterThatCannotBeReadLeavesNoCapacityBehind(t *testing.T) {
	standIns(t, capacity.Nothing, capacity.Nothing)
	ctx := &run.Context{Root: run.Root{Dir: t.TempDir()}, RepoRoot: t.TempDir(), Site: "sitex"}
	out := filepath.Join(t.TempDir(), "as-built")
	err := recordCapacity(ctx, out, time.Now())
	if err == nil || !strings.Contains(err.Error(), "none is a worker") {
		t.Fatalf("a cluster with no workers was recorded, or refused for another reason: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(out, capacity.File)); statErr == nil {
		t.Error("a capacity file was written for a cluster that could not be read")
	}
}
