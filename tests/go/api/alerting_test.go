//go:build api

package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"homelab/tests/harness"
)

// The alert destination answers, without saying anything in the channel.
//
// WHAT THIS IS FOR. Alertmanager holds one webhook URL and posts to it when
// something is wrong. If that URL has been revoked, or the app removed from the
// workspace, nothing anywhere notices - alerts simply stop arriving, and the
// silence is indistinguishable from an estate with nothing wrong. This is the
// one question that distinguishes them, and it is a question only the vendor
// can answer, which is why it lives in this tier rather than in tests/go/repo.
//
// HOW IT ASKS WITHOUT SHOUTING. An incoming webhook rejects a malformed body
// with 400 and `invalid_payload` before it delivers anything, and answers a
// revoked or unknown URL with 404 and `no_service`. So an empty body is a live
// check that posts no message: a channel full of test messages is a channel
// people mute, which would defeat the thing being tested.
//
// Confirmed against the real API rather than assumed. If Slack ever starts
// answering 200 to a malformed body, this fails and the check needs rewriting
// rather than deleting - the assumption it rests on will have changed.
//
// covers: api:alerting
func TestTheAlertDestinationIsLive(t *testing.T) {
	alerting := harness.Alerting(t)
	require.Equal(t, "slack", alerting.Provider,
		"the rendered config declares alerting.provider %q, and this test knows how to probe Slack. "+
			"A different vendor needs its own live check rather than this one passing by accident.",
		alerting.Provider)
	require.NotEmpty(t, alerting.WebhookURL,
		"the rendered config has no alerting.webhook_url, so Alertmanager has nowhere to post and "+
			"every alert this estate raises would be lost in silence")

	client := &http.Client{Timeout: 15 * time.Second}
	// Deliberately malformed: rejected before anything is delivered.
	resp, err := client.Post(alerting.WebhookURL, "application/json", strings.NewReader(""))
	require.NoError(t, err, "posting to the alert destination")
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "reading the destination's answer")

	// The URL is a credential; the answer is not, but a failure message that
	// echoed the request would carry one into a job summary.
	answer := strings.TrimSpace(string(body))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode,
		"the alert destination answered %d (%q) rather than 400.\n\n"+
			"404 with no_service means the webhook has been revoked or its app removed, and every "+
			"alert since then went nowhere. 200 means Slack accepted an empty body, which would mean "+
			"this check no longer proves anything and needs rewriting.", resp.StatusCode, answer)
	assert.Equal(t, "invalid_payload", answer,
		"the alert destination answered %q rather than invalid_payload, so the assumption this "+
			"check rests on has changed", answer)
}
