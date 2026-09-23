package run

import "testing"

// The decision that releases a resource from state, and every way it must
// refuse to.
//
// Releasing is not destructive to the resource itself, but it stops the estate
// tracking something real - so a false positive here produces exactly the
// landmine this repository refuses: a live thing nothing knows about. Every
// case below that returns false is a case where the honest answer is "cannot
// tell", and telling them apart from "renamed" is the whole job.
func TestShouldReleaseOnlyOnAKnownRename(t *testing.T) {
	for _, tc := range []struct {
		name       string
		have, want string
		release    bool
		why        string
	}{
		{
			name: "a genuine rename",
			have: "old-database", want: "example-database", release: true,
			why: "the tracked name and the configured name are both known and differ",
		},
		{
			name: "nothing changed",
			have: "example-database", want: "example-database", release: false,
			why: "the ordinary case on every converge that changes nothing",
		},
		{
			name: "the tracked name could not be read",
			have: "", want: "example-database", release: false,
			why: "a parse failure, or a provider that stopped printing the attribute, is not " +
				"evidence of a rename - releasing here would drop a resource nobody renamed",
		},
		{
			name: "the config resolved to no name",
			have: "example-database", want: "", release: false,
			why: "a config that produced no name is one to stop on, not one to release against",
		},
		{
			name: "neither is known",
			have: "", want: "", release: false,
			why: "two unknowns are not a difference",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRelease(tc.have, tc.want); got != tc.release {
				t.Errorf("shouldRelease(%q, %q) = %v, want %v\n\n%s",
					tc.have, tc.want, got, tc.release, tc.why)
			}
		})
	}
}
