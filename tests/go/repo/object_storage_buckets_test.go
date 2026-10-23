package repo

import (
	"sort"
	"strings"
	"testing"

	"homelab/contractor/config"
	"homelab/tests/parsed"
)

// The estate creates a site's buckets and the contractor uses them, and the two
// must agree about which buckets there are.
//
// management/estate/site/object-storage.tf creates one bucket per purpose and
// grants a key for each; buckets.go is how the contractor knows which granted
// bucket is which, and which one a teardown empties. Neither can be derived
// from the other - OpenTofu cannot read a Go table - so they are written twice
// and held together here.
//
// A purpose the estate creates and the table lacks is a bucket the site is
// granted and never uses, and never empties. A purpose the table has and the
// estate lacks is a credential the site's config asks the vault for and never
// receives, so every run fails at Render naming a field rather than a bucket.
func TestTheBucketTableAgreesWithTheHCL(t *testing.T) {
	hcl := bucketPurposesInHCL(t)
	var table []string
	for _, b := range config.Buckets {
		table = append(table, b.Key)
	}
	sort.Strings(table)
	if strings.Join(hcl, " ") != strings.Join(table, " ") {
		t.Errorf("the estate creates buckets for %v and buckets.go knows %v.\n\n"+
			"A purpose on one side only is a bucket granted and never used, or a grant the "+
			"site asks for and never receives. Change both.", hcl, table)
	}
}

// bucketPurposesInHCL is the purposes the estate creates a site's buckets
// for: the local that declares them, read as the strings it is written as.
func bucketPurposesInHCL(t *testing.T) []string {
	t.Helper()
	tree := repository(t)
	out, ok := parsed.Strings(tree.Local(t, "purposes").Expr)
	if !ok {
		t.Fatal("local.purposes is not a set of strings written out, so which buckets the estate creates cannot be read from it")
	}
	if len(out) == 0 {
		t.Fatal("the estate declares an empty set of bucket purposes, so this guard compares nothing")
	}
	sort.Strings(out)
	return out
}
