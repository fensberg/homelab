package config

// A site is two OpenTofu roots, each a directory under management/ with its
// own state (docs/epochs/02-abstraction.md, "Inside a site, the split is two
// roots sharing one config").
//
// The cluster root builds the machines and ends at "the nodes are Ready". The
// platform root starts there: it puts on the cluster what Flux cannot put
// there itself, and its provider is configured from the cluster root's
// outputs rather than from a resource of its own.
//
// Named here, once, because the contractor, its declared steps, the backup
// layout and the test tiers all say which root they mean.
const (
	ClusterRoot  = "cluster"
	PlatformRoot = "platform"
)

// Roots is both, in the order they are applied.
var Roots = []string{ClusterRoot, PlatformRoot}
