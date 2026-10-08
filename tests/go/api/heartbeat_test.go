//go:build api

package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"homelab/tests/harness"
)

// The watchman is at the address the site was granted, and turns away a ring
// that is not the site's.
//
// WHAT THIS IS FOR. The site's alerting rings an address outside the site
// every ten minutes, and the party there says in the channel when the ringing
// stops. If that party is gone - the script removed, the account's address
// changed - the rings go nowhere and nothing is watching for their absence,
// which looks exactly like a site that is well. Whether it is there is a
// question only the vendor's edge can answer.
//
// WHY IT NEVER RINGS WITH THE SITE'S OWN SECRET. A ring is the site saying it
// is alive. One sent from here would say so on the site's behalf: it would
// hide a site whose alerting had really stopped for as long as this test kept
// running, and on a site that does not ring yet it would start a clock that
// ends with the channel being told the site has gone quiet. So this asks only
// what can be asked without being heard - that a wrong secret is refused, and
// that anything but a ring is refused differently. Both are answers the
// watchman's own code gives and nothing else at that address would. That a
// real ring is accepted is proved from inside the site, by Alertmanager's own
// count of the rings it sent and the ones that failed.
//
// covers: api:heartbeat
func TestTheWatchmanIsThereAndTurnsAwayARingThatIsNotTheSites(t *testing.T) {
	heartbeat := harness.SiteConfig(t).Heartbeat
	require.Equal(t, "cloudflare", heartbeat.Provider,
		"the rendered config declares the site's heartbeat.provider as %q, and this test knows the watchman the "+
			"estate runs at Cloudflare. A different party needs its own check of what it refuses.", heartbeat.Provider)
	require.NotEmpty(t, heartbeat.URL,
		"the rendered config has no address for the site to ring, so nothing outside the site would notice it stop")

	client := &http.Client{Timeout: 15 * time.Second}
	ask := func(method, bearer string) int {
		req, err := http.NewRequest(method, heartbeat.URL, nil)
		require.NoError(t, err)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := client.Do(req)
		require.NoError(t, err, "the watchman's address did not answer at all")
		defer resp.Body.Close()
		return resp.StatusCode
	}

	assert.Equal(t, http.StatusUnauthorized, ask(http.MethodPost, "not-what-this-site-rings-with"),
		"a ring with the wrong secret was not turned away as one. Either the watchman is not what answers at "+
			"this address, or it would let anybody keep a silent site looking alive.")
	assert.Equal(t, http.StatusUnauthorized, ask(http.MethodPost, ""),
		"a ring with no secret was not turned away as one")
	assert.Equal(t, http.StatusMethodNotAllowed, ask(http.MethodGet, ""),
		"something other than a ring was not refused as that, so what answers here is not the watchman's code")
}
