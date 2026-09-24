module homelab/clerk

go 1.26

// Standard details, from this checkout - test-only here, so nothing in it
// reaches the binary that holds the clerk App's private key.
require homelab/details v0.0.0

replace homelab/details => ../details
