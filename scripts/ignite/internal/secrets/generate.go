// Package secrets generates the credentials this project owns end to end -
// the ones nobody ever reads, types, or needs to recognise.
//
// The rule that decides what belongs here: rotate what lands in OpenTofu
// state. A leaked state file yields whatever is stored as a resource
// attribute, so those credentials have to be worth nothing by the time anyone
// finds the file. Secrets that only ever configure a provider - the Proxmox
// token, the Tailscale OAuth secret, the Cloudflare admin token - are never
// written to state at all, and are a human's to manage.
package secrets

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// Deliberately narrow. This password is spliced into
// postgres://tofu:<pw>@host:port/db?sslmode=require, and phases/sterilize.go
// parses a host and port back out of that string with a regex. Any of
// @ : / ? # [ ] % & = would change what the URI means - sometimes into
// something that still parses and points somewhere else, which is the worst
// available outcome. Letters and digits only, no exceptions worth the risk.
//
// 62 characters is ~5.95 bits each, so the 32-character default carries ~190
// bits. Losing symbols costs about 8 bits against a printable-ASCII alphabet
// and buys a credential that cannot corrupt the string it lives in.
const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// minLength is a floor rather than a default: a caller asking for something
// shorter has made a mistake, and silently obliging is how a weak credential
// reaches production on the one run nobody watches.
const minLength = 16

// Password returns a cryptographically random password of exactly n
// characters, safe to embed in a URI without escaping.
func Password(n int) (string, error) {
	if n < minLength {
		return "", fmt.Errorf("password length %d is below the %d-character minimum", n, minLength)
	}
	max := big.NewInt(int64(len(alphabet)))
	out := make([]byte, n)
	for i := range out {
		// crypto/rand, not math/rand: this is the only credential standing
		// between a leaked state file and the state itself.
		k, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("reading from the system random source: %w", err)
		}
		out[i] = alphabet[k.Int64()]
	}
	return string(out), nil
}
