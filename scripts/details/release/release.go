// Package release is the shape of a production release: what its tag and its
// digest look like.
//
// Three parties handle one - the fabricator numbers it, procurement moves the
// pin to it, and the repository's tests refuse a pin that is not one - so the
// shape is drawn here, once, and each of them reads it.
package release

import "regexp"

// Digest is how a release is pinned: the artifact's own sha256, which no
// registry push can move.
var Digest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Tag is how a release is named: the version the workload reports, a hyphen,
// and a counter for rebuilds of that version - 1.0.15-6.
var Tag = regexp.MustCompile(`^[0-9][0-9A-Za-z.]*-[0-9]+$`)
