package phases

import (
	"reflect"
	"strings"
	"testing"
)

// The first real teardown of a fully-ignited estate destroyed nothing at all.
// Two separate reasons, both structural rather than transient, and both fixed
// by the two functions tested here:
//
//  1. The R2 bucket had seven objects in it, and Cloudflare refuses to delete
//     a bucket that is not empty. tofu asked, R2 said no, and the whole
//     destroy aborted.
//  2. Deleting the flux-system namespace hung until "context deadline
//     exceeded". Flux puts finalizers on its own Kustomizations and
//     HelmReleases; deleting the namespace blocks on controllers that are
//     themselves being torn down.
//
// The second one is worth stating plainly: there is no reason to gracefully
// delete Kubernetes objects inside VMs that are about to be deleted. They stop
// existing when the disks do.

func TestClusterInternalAddresses_PicksTheKubernetesResources(t *testing.T) {
	stateList := `data.talos_client_configuration.this
kubernetes_namespace.database
kubernetes_namespace.flux_system
kubernetes_secret.cluster_vars
kubernetes_secret.flux_system_git_auth
kubernetes_secret.object_storage_credentials
kubernetes_secret.state_db_credentials
proxmox_virtual_environment_vm.talos_cp[0]
talos_machine_bootstrap.this
terraform_data.flux_bootstrap_apply
`
	got := clusterInternalAddresses(stateList)
	want := []string{
		"kubernetes_namespace.database",
		"kubernetes_namespace.flux_system",
		"kubernetes_secret.cluster_vars",
		"kubernetes_secret.flux_system_git_auth",
		"kubernetes_secret.object_storage_credentials",
		"kubernetes_secret.state_db_credentials",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

// The VMs, the disk image, the R2 bucket and the tailnet key are the whole
// point of the destroy. Forgetting any of them leaves a real object running
// that nothing tracks, which is strictly worse than the deadlock this avoids.
func TestClusterInternalAddresses_LeavesEverythingWithARealRemoteObject(t *testing.T) {
	mustKeep := []string{
		"proxmox_virtual_environment_vm.talos_cp[0]",
		"proxmox_virtual_environment_vm.talos_template",
		"proxmox_download_file.talos_disk_image",
		"cloudflare_r2_bucket.homelab",
		"tailscale_tailnet_key.hypervisor",
		"talos_machine_secrets.this",
	}
	got := clusterInternalAddresses(strings.Join(mustKeep, "\n"))
	if len(got) != 0 {
		t.Errorf("these must never be forgotten, but %v were selected", got)
	}
}

func TestClusterInternalAddresses_IgnoresBlankLinesAndWhitespace(t *testing.T) {
	got := clusterInternalAddresses("\n  kubernetes_namespace.database  \n\n")
	if !reflect.DeepEqual(got, []string{"kubernetes_namespace.database"}) {
		t.Errorf("got %v", got)
	}
}

// An empty state list is not an error - a run that failed before it reached
// the cluster has no Kubernetes resources to forget, and the teardown must
// carry on to the VMs.
func TestClusterInternalAddresses_EmptyStateIsNotAnError(t *testing.T) {
	if got := clusterInternalAddresses(""); len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

// A data source is not a managed resource: `tofu state rm` on one fails, and
// there is nothing to forget anyway.
func TestClusterInternalAddresses_SkipsDataSources(t *testing.T) {
	if got := clusterInternalAddresses("data.kubernetes_namespace.database"); len(got) != 0 {
		t.Errorf("data sources must not be selected, got %v", got)
	}
}
