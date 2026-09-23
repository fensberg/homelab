package run

import "testing"

// The name is read off the exact attribute, not off anything ending in "name".
//
// A `tofu state show` prints every attribute of a resource, and several
// providers carry more than one that ends in the word. Matching loosely would
// read the wrong value and then release a resource because two unrelated
// strings differed.
func TestTrackedNameMatchesOnlyTheNameAttribute(t *testing.T) {
	for _, tc := range []struct {
		line string
		want string
	}{
		{`    name        = "example-database"`, "example-database"},
		{`    name = "x"`, "x"},
		{`    name        = ""`, ""},
	} {
		m := trackedName.FindStringSubmatch(tc.line)
		if m == nil {
			t.Errorf("did not match a name line: %q", tc.line)
			continue
		}
		if m[1] != tc.want {
			t.Errorf("read %q from %q, want %q", m[1], tc.line, tc.want)
		}
	}

	for _, line := range []string{
		`    bucket_name = "wrong"`,
		`    display_name = "wrong"`,
		`    name_prefix = "wrong"`,
		`    account_id  = "wrong"`,
		`  # name = "commented"`,
	} {
		if m := trackedName.FindStringSubmatch(line); m != nil {
			t.Errorf("matched %q and read %q; only the bare `name` attribute may match, "+
				"because reading the wrong attribute here releases a resource that was "+
				"never renamed", line, m[1])
		}
	}
}
