package release

import "testing"

func TestTheShapesAcceptReleasesAndNothingElse(t *testing.T) {
	d := "sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, ok := range []string{d} {
		if !Digest.MatchString(ok) {
			t.Errorf("Digest refused %q", ok)
		}
	}
	for _, bad := range []string{"sha256:abc", "latest", d + "0", "SHA256:" + d[7:]} {
		if Digest.MatchString(bad) {
			t.Errorf("Digest accepted %q", bad)
		}
	}
	for _, ok := range []string{"1.0.15-6", "0.217.46-1"} {
		if !Tag.MatchString(ok) {
			t.Errorf("Tag refused %q", ok)
		}
	}
	for _, bad := range []string{"1.0.15", "latest", "v1.0.15-1", "1.0.15-"} {
		if Tag.MatchString(bad) {
			t.Errorf("Tag accepted %q", bad)
		}
	}
}
