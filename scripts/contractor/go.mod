module homelab/contractor

go 1.26

// Standard details, from this checkout. A local replace, so the contractor's
// zero-dependency property holds: nothing is fetched, nothing enters a go.sum,
// and scripts/details carries no dependencies of its own to bring with it.
require homelab/details v0.0.0

replace homelab/details => ../details
