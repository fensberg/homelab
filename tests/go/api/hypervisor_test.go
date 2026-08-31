//go:build api

// Package api_test holds contract tests against the live vendor APIs this
// project talks to.
//
// These are not tests of our code. They are tests of an assumption our code
// makes about somebody else's service - the kind of assumption that is
// verified once, written into a comment, and then quietly becomes false a
// year later when the vendor ships a change. Every test here corresponds to a
// specific line of production code that would break if the answer changed.
//
//	go test -tags=api ./api/...
package api_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"homelab/tests/harness"
)

// hypervisorClient mirrors the one in scripts/contractor/internal/phases/compute.go,
// including its InsecureSkipVerify: Proxmox serves a self-signed certificate
// and versions.tf's provider block already accepts it with insecure = true.
// A test that verified the certificate would fail for a reason that has
// nothing to do with what it is testing.
func hypervisorClient() *http.Client {
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}, //nolint:gosec
		},
	}
}

func getJSON(t *testing.T, url, authHeader string) (int, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err, "building the request")
	req.Header.Set("Authorization", authHeader)

	resp, err := hypervisorClient().Do(req)
	require.NoError(t, err, "the hypervisor API is unreachable from this runner")
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "reading the response body")
	return resp.StatusCode, body
}

// findTalosDiskImage in compute.go parses exactly this response to decide
// whether a prior run left a disk image behind that should be adopted rather
// than recreated. It reads .data[].volid and matches on the volid's shape. If
// the envelope or the field name ever changes, adoption silently stops
// finding anything - and the failure surfaces much later, as an apply trying
// to create a file that is already there.
func TestHypervisorDatastoreContentEnvelopeIsUnchanged(t *testing.T) {
	site := harness.SiteConfig(t)
	hostname, ip := harness.FirstHypervisor(t)

	url := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/storage/local-iso/content", ip, hostname)
	auth := fmt.Sprintf("PVEAPIToken=%s=%s", site.Hypervisor.TokenID, site.Hypervisor.TokenSecret)

	status, body := getJSON(t, url, auth)
	require.Equal(t, http.StatusOK, status,
		"listing the local-iso datastore failed; if this is a 401 the API token in the vault no longer carries Datastore.Audit")

	var parsed struct {
		Data []struct {
			VolID string `json:"volid"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &parsed),
		"the response is no longer a {\"data\": [...]} envelope, which is the exact shape findTalosDiskImage unmarshals into")

	// An empty datastore is a legitimate state - nothing has been downloaded
	// yet - so the assertion is about the shape of what is there, not about
	// there being anything.
	for _, item := range parsed.Data {
		assert.NotEmpty(t, item.VolID,
			"an entry came back with no volid; compute.go uses volid as the Terraform import ID and an empty one would import nothing")
		assert.Contains(t, item.VolID, ":",
			"volid %q is not the \"datastore:content_type/file_name\" shape adoptOrphanedDiskImage splices a node name onto", item.VolID)
	}
}

// Both the Verify and Compute phases decide the hypervisor is usable by
// opening a TCP connection to 8006 and nothing more. This confirms that the
// port answering actually means the API answers, rather than something else
// having taken the port.
func TestHypervisorAPIPortServesTheAPI(t *testing.T) {
	site := harness.SiteConfig(t)
	hostname, ip := harness.FirstHypervisor(t)

	url := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/status", ip, hostname)
	auth := fmt.Sprintf("PVEAPIToken=%s=%s", site.Hypervisor.TokenID, site.Hypervisor.TokenSecret)

	status, body := getJSON(t, url, auth)
	require.Equal(t, http.StatusOK, status, "node status: %s", body)

	var parsed struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &parsed))
	assert.NotEmpty(t, parsed.Data, "the API answered on 8006 but returned no node status")
}
