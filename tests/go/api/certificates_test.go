//go:build api

package api_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	cf "homelab/details/cloudflare"
	"homelab/tests/harness"
)

// The credential the estate granted for certificates reaches the estate's
// own domain, and that domain alone.
//
// cert-manager proves a name to the issuer by a record under the domain, with
// this credential. One that reaches no domain gets no certificate, and says
// so only inside the cluster, days later, when the first one is asked for.
// One that reaches more than the one domain is a grant wider than the estate
// meant to make: the vendor scopes one to a whole domain and no narrower,
// so the count of domains is the narrowest thing there is to hold it to.
//
// covers: api:certificates
func TestTheGrantedCertificatesCredentialReachesTheEstatesDomainAlone(t *testing.T) {
	held := harness.Certificates(t)
	require.Equal(t, "cloudflare", held.Provider,
		"the rendered config declares certificates.provider %q, and this test knows how to ask Cloudflare "+
			"what a credential reaches. A different vendor needs its own check rather than this one passing by accident.",
		held.Provider)
	require.NotEmpty(t, held.DNSToken, "the rendered config has no certificates.dns_token")

	zones, err := cf.Get[[]struct {
		Name string `json:"name"`
	}](cf.API, held.DNSToken, "/zones", nil)
	require.NoError(t, err, "asking the vendor which domains the granted credential reaches")
	require.Len(t, zones, 1, "the granted credential reaches %d domain(s), and is granted for exactly one", len(zones))
	require.Equal(t, harness.LoadConfig(t).Organization.Domain, zones[0].Name,
		"the one domain the granted credential reaches is not the estate's own")
}
