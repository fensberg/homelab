package phases

// ok: the-contractor-writes-a-resource-address-in-one-place
// talos_cluster_kubeconfig.this, in a comment
var (
	// ruleid: the-contractor-writes-a-resource-address-in-one-place
	a = "talos_cluster_kubeconfig.this"
	// ruleid: the-contractor-writes-a-resource-address-in-one-place
	b = "data.talos_cluster_health.this[0]"
	// ok: the-contractor-writes-a-resource-address-in-one-place
	c = "talos_version"
	// ok: the-contractor-writes-a-resource-address-in-one-place
	d = `tofu apply (compute: vms)`
)
