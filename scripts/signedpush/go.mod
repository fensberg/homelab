// Beside ignite, never inside it.
//
// ignite can destroy infrastructure; this pushes commits. Sharing a module
// would mean a bug in one is a rebuild of the other, and it would put this
// program's failure modes inside the binary that holds the estate. They are
// different jobs with different blast radii, so they are different modules.
//
// Zero dependencies, same as ignite and for the same reason: this program
// reads the GitHub App private key, and a supply chain that can reach the key
// is a supply chain that can mint tokens for this repository. crypto/rsa,
// net/http and encoding/json are all it needs.
module homelab/signedpush

go 1.26.0
