package main

import (
	"strings"
	"testing"
)

// Adoption matches what the plan would create against what the account holds,
// by name, and takes the address from the plan - so the naming lives in the HCL
// alone. Only creates count: a bucket the plan keeps or replaces is not one
// that outlived the last estate.
func TestPlannedCreatesMapsEachNewBucketsNameToItsAddress(t *testing.T) {
	shown := []byte(`{"format_version":"1.2","resource_changes":[
		{"address":"module.site.cloudflare_r2_bucket.created","type":"cloudflare_r2_bucket",
		 "change":{"actions":["create"],"after":{"name":"example-site-state"}}},
		{"address":"module.site.cloudflare_r2_bucket.kept","type":"cloudflare_r2_bucket",
		 "change":{"actions":["no-op"],"after":{"name":"example-site-database"}}},
		{"address":"cloudflare_account_token.x","type":"cloudflare_account_token",
		 "change":{"actions":["create"],"after":{"name":"site0 state bucket"}}}]}`)
	got, err := plannedCreates(shown, bucketType)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["example-site-state"] != "module.site.cloudflare_r2_bucket.created" {
		t.Fatalf("got %v; want only the created bucket, by name", got)
	}

	_, err = plannedCreates([]byte(`{"resource_changes":[{"address":"a","type":"cloudflare_r2_bucket","change":{"actions":["create"],"after":{}}}]}`), bucketType)
	if err == nil {
		t.Error("a bucket created with no known name was accepted, so an existing one could be missed and created twice")
	}
}

// A demolish releases every bucket, however deep in a module it is declared,
// and nothing else.
func TestBucketAddressesFindsEveryBucketAndOnlyBuckets(t *testing.T) {
	got := bucketAddresses([]string{
		`module.site["site0"].cloudflare_r2_bucket.production`,
		`cloudflare_r2_bucket.legacy`,
		`module.site["site0"].cloudflare_account_token.production`,
		`cloudflare_zero_trust_access_application.enrollment`,
	})
	if len(got) != 2 || !strings.Contains(got[0], "cloudflare_r2_bucket") || !strings.Contains(got[1], "cloudflare_r2_bucket") {
		t.Fatalf("got %v", got)
	}
}

// Every grant becomes a vault field, so each must be a non-empty string; a
// value of another shape is refused rather than written as something else.
func TestGrantsAreReadSiteThenItemThenField(t *testing.T) {
	got, err := grantsFrom([]byte(`{"site0":{"tunnel":{"provider":"cloudflare","token":"t"},"identity":{"name":"n"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got["site0"]["tunnel"]["token"] != "t" || got["site0"]["identity"]["name"] != "n" {
		t.Fatalf("got %v", got)
	}
	for _, bad := range []string{
		`{"site0":{"tunnel":{"token":""}}}`,
		`{"site0":{"tunnel":{"token":7}}}`,
		`not json`,
	} {
		if _, err := grantsFrom([]byte(bad)); err == nil {
			t.Errorf("%s was accepted as grants", bad)
		}
	}
}
