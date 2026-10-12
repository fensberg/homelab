module homelab/notary

go 1.26.0

// Standard details, from this checkout, as the lawyer takes them. And the Go
// team's own reading of the protocol a certificate is asked for by: the one
// thing here not written in this repository, because the protocol is signed
// request by request and a second implementation of it is one nobody audits.
require (
	golang.org/x/crypto v0.58.0
	homelab/details v0.0.0
)

replace homelab/details => ../details
