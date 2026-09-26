package procenv

import (
	"slices"
	"testing"
)

// An inherited variable in a namespace the program configures is dropped;
// the program's own settings and every unrelated variable survive (#486).
func TestAProgramOwnsTheWholeNamespaceItConfigures(t *testing.T) {
	base := []string{"PATH=/bin", "RCLONE_VERSION=1.2.3", "RCLONE_CONFIG_R2_ACCESS_KEY_ID=inherited", "AWS_PROFILE=someone"}
	got := With(base, []string{"RCLONE_CONFIG_R2_ACCESS_KEY_ID=mine"})

	for _, want := range []string{"PATH=/bin", "RCLONE_CONFIG_R2_ACCESS_KEY_ID=mine", "AWS_PROFILE=someone"} {
		if !slices.Contains(got, want) {
			t.Errorf("%q is missing from %v", want, got)
		}
	}
	for _, gone := range []string{"RCLONE_VERSION=1.2.3", "RCLONE_CONFIG_R2_ACCESS_KEY_ID=inherited"} {
		if slices.Contains(got, gone) {
			t.Errorf("%q was inherited into a namespace the program owns: %v", gone, got)
		}
	}

	got = With(base, []string{"AWS_ACCESS_KEY_ID=mine"})
	if slices.Contains(got, "AWS_PROFILE=someone") {
		t.Error("an inherited AWS_PROFILE survived beside the backend's own credential")
	}
	if !slices.Contains(got, "RCLONE_VERSION=1.2.3") {
		t.Error("a namespace the program did not touch was stripped")
	}
}
