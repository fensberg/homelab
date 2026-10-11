package phases

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"homelab/contractor/internal/run"
	"homelab/details/applications"
	"homelab/details/capacity"
)

// recordCapacity takes a snapshot of the site as its scheduler sees it, and
// keeps it beside the record.
//
// Here, at the end of taking a record, because this is after the cluster
// has settled: a converge has applied its change and waited for it, and a
// snapshot taken now is the site as that change left it. A pull request's
// plan holds no credential and cannot ask the cluster anything, so this is
// what it tries a change against.
//
// Asked of the cluster itself, the way the health phase asks it. Only the
// cluster knows what is running and where. ctx is the cluster root's, whose
// output the kubeconfig is.
func recordCapacity(ctx *run.Context, dir string, taken time.Time) error {
	kubeconfig, cleanup, err := writeKubeconfig(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	var listings capacity.Listings
	for list, into := range map[string]*[]byte{
		"nodes": &listings.Nodes, "pods": &listings.Pods, "namespaces": &listings.Namespaces,
		"persistentvolumes": &listings.Volumes, "persistentvolumeclaims": &listings.Claims,
		"priorityclasses": &listings.PriorityClasses, "storageclasses": &listings.StorageClasses,
	} {
		if *into, err = kubectl(ctx, kubeconfig, "get", list, "--all-namespaces", "-o", "json"); err != nil {
			return err
		}
	}
	// An application's namespace is named for the application, which the
	// repository spells: its pods keep the name of the workload they belong
	// to, so that a change to it can be tried as the pods it really makes.
	given, err := applications.Assigned(ctx.RepoRoot, ctx.Site)
	if err != nil {
		return err
	}
	var spelled []string
	for _, a := range given {
		spelled = append(spelled, a.Application)
	}
	snapshot, err := capacity.Take(listings, spelled, taken.UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	raw, err := capacity.Marshal(snapshot)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, capacity.File), append(raw, '\n'), 0o644); err != nil {
		return err
	}
	// How near the edge the site is running, said every time a record is
	// taken.
	run.Info(snapshot.String())
	run.Ok(fmt.Sprintf("the site's capacity is saved beside the record, in %s", capacity.File))
	return nil
}
