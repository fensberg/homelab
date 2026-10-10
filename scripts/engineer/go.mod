module homelab/engineer

go 1.26

// Standard details, from this checkout, as the contractor takes them: a local
// replace, and scripts/details carries no dependencies of its own. The
// engineer takes none either. What it reads of the estate is redrawn for it
// by the drafter, which is the one program that takes a library to read a
// format with.
require homelab/details v0.0.0

replace homelab/details => ../details
