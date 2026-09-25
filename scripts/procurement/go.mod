module homelab/procurement

go 1.26

// Standard details, from this checkout, as the contractor takes them: a local
// replace, and scripts/details carries no dependencies of its own.
require homelab/details v0.0.0

replace homelab/details => ../details
