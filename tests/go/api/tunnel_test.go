//go:build api

package api_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"homelab/tests/harness"
)

// The run token the estate granted is a tunnel token for this estate's
// account.
//
// A site holds the run token alone now - the estate creates the tunnel, and
// the token serves that one tunnel and can ask the API nothing. So whether the
// tunnel is CONNECTED is a question for the estate's token, and belongs to the
// estate's lane (#535). What a site can check is that its grant is a real run
// token, for the account its buckets are in: a token from another account, or
// a value pasted into the wrong field, would leave cloudflared failing to
// connect with nothing naming the grant.
//
// covers: api:tunnel
func TestTheGrantedTunnelTokenIsForThisEstatesAccount(t *testing.T) {
	tunnel := harness.Tunnel(t)
	require.Equal(t, "cloudflare", tunnel.Provider,
		"the rendered config declares tunnel.provider %q, and this test knows how to read a Cloudflare "+
			"run token. A different vendor needs its own check rather than this one passing by accident.",
		tunnel.Provider)
	require.NotEmpty(t, tunnel.Token, "the rendered config has no tunnel.token")

	raw, err := base64.StdEncoding.DecodeString(tunnel.Token)
	require.NoError(t, err, "the granted tunnel token is not base64, so it is not a Cloudflare run token")
	var token struct {
		Account string `json:"a"`
		Tunnel  string `json:"t"`
		Secret  string `json:"s"`
	}
	require.NoError(t, json.Unmarshal(raw, &token), "the granted tunnel token does not decode to a run token")
	require.NotEmpty(t, token.Tunnel, "the granted run token names no tunnel")
	require.NotEmpty(t, token.Secret, "the granted run token carries no secret")
	require.Equal(t, harness.SiteConfig(t).ObjectStorage.AccountID, token.Account,
		"the granted run token is for a different account than this site's buckets")
}
