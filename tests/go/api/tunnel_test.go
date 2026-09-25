//go:build api

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"homelab/details/cloudflare"
	"homelab/tests/harness"
)

// The tunnel exists at the vendor, and its connector is connected.
//
// WHAT THIS IS FOR. Everything an enrolled device reaches from off the LAN
// arrives through one Cloudflare Tunnel, carried by cloudflared pods that dial
// out to Cloudflare. If the token is revoked, the tunnel deleted in the
// dashboard, or every connector unable to reach the edge, the estate looks
// exactly as it did - pods Running, Services present - and every remote join
// times out. Only Cloudflare can say whether the tunnel is up, which is why
// this lives in this tier rather than in tests/go/repo.
//
// It asks with the tunnel's own token, so it also proves that token can still
// read what it manages.
//
// covers: api:tunnel
func TestTheTunnelIsDeclaredAtTheVendorAndConnected(t *testing.T) {
	tunnel := harness.Tunnel(t)
	require.Equal(t, "cloudflare", tunnel.Provider,
		"the rendered config declares tunnel.provider %q, and this test knows how to ask Cloudflare. "+
			"A different vendor needs its own live check rather than this one passing by accident.",
		tunnel.Provider)
	require.NotEmpty(t, tunnel.APIToken, "the rendered config has no tunnel.api_token")

	account := harness.ObjectStorageAccount(t).AccountID
	name := fmt.Sprintf("%s-%s", harness.LoadConfig(t).Organization.Name, harness.Site())

	q := url.Values{"name": {name}, "is_deleted": {"false"}}
	req, err := http.NewRequest(http.MethodGet,
		"https://api.cloudflare.com/client/v4/accounts/"+account+"/cfd_tunnel?"+q.Encode(), nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+tunnel.APIToken)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err, "asking Cloudflare for the tunnel")
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"Cloudflare answered %d to the tunnel token listing its own tunnels. The token may have "+
			"been revoked or lost the Cloudflare Tunnel read permission.", resp.StatusCode)

	var answer cloudflare.Answer[[]struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}]
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&answer), "decoding Cloudflare's answer")
	require.True(t, answer.Success, "Cloudflare reported the tunnel listing as unsuccessful")
	require.Len(t, answer.Result, 1,
		"Cloudflare holds %d live tunnels named %q; management/cluster/tunnel.tf declares exactly one",
		len(answer.Result), name)

	// healthy: every connector up. degraded: some. inactive and down mean no
	// connector is carrying anything, which is the silent failure above.
	status := answer.Result[0].Status
	require.Contains(t, []string{"healthy", "degraded"}, status,
		"the tunnel %q is %q: no cloudflared connector is carrying traffic, so every enrolled "+
			"device's route leads nowhere. Check the cloudflared pods in the tunnel namespace.",
		name, status)
}
