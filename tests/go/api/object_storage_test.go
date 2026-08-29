//go:build api

package api_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"homelab/tests/harness"
)

// adoptOrphanedR2Bucket in cluster.go turns on a single documented fact,
// recorded there as a comment:
//
//	"Confirmed against the real API, not assumed: a missing bucket is a
//	 genuine 404 ({...\"The specified bucket does not exist.\"}), not a 200
//	 with an error body."
//
// That confirmation was true on the day it was written. This is what keeps it
// true: if the vendor ever starts answering 200-with-an-error-body, the switch
// in r2BucketExists falls through to its default branch and the whole Cluster
// phase fails with "HTTP 200" - during a run that has already created VMs.
func TestObjectStorageMissingBucketIsA404(t *testing.T) {
	acct := harness.ObjectStorageAccount(t)
	require.NotEmpty(t, acct.AccountID, "the rendered config has no object_storage.account_id")
	require.NotEmpty(t, acct.AdminToken, "the rendered config has no object_storage.admin_token")

	// A name that cannot exist: bucket names are lowercase alphanumerics and
	// hyphens, so this is safe to probe without any chance of naming
	// something real.
	missing := fmt.Sprintf("homelab-test-absent-%d", time.Now().UnixNano())

	status := headBucket(t, acct.AccountID, acct.AdminToken, missing)
	require.Equal(t, http.StatusNotFound, status,
		"a missing bucket answered %d, not 404. r2BucketExists treats anything that is neither 200 nor 404 as a hard error, so the Cluster phase would now fail mid-run rather than adopting or creating the bucket.", status)
}

// The other half of the same switch: an existing bucket must answer 200. If
// it started answering 204 or 301, orphan adoption would conclude the bucket
// does not exist and apply would try to create one that is already there.
func TestObjectStorageExistingBucketIsA200(t *testing.T) {
	site := harness.SiteConfig(t)
	acct := harness.ObjectStorageAccount(t)
	require.NotEmpty(t, site.ObjectStorage.Bucket, "the rendered config has no object_storage.bucket")

	status := headBucket(t, acct.AccountID, acct.AdminToken, site.ObjectStorage.Bucket)
	require.Equal(t, http.StatusOK, status,
		"the configured bucket %q answered %d. Either it has not been created yet - run the Cluster phase - or the API's success status has changed.", site.ObjectStorage.Bucket, status)
}

func headBucket(t *testing.T, accountID, token, bucket string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/r2/buckets/%s", accountID, bucket)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	require.NoError(t, err, "the object storage API is unreachable from this runner")
	defer resp.Body.Close()
	return resp.StatusCode
}
