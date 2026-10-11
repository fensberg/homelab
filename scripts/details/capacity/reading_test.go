package capacity

import "testing"

func TestASizeThatCannotBeReadIsRefused(t *testing.T) {
	for _, written := range []string{"", "lots", "12Qi", "-1Gi", "1.5.2Gi"} {
		if _, err := Bytes(written); err == nil {
			t.Errorf("%q was read as a size", written)
		}
	}
	for written, want := range map[string]int64{
		"9663676416": 9663676416, "9437184Ki": 9437184 << 10, "512Mi": 512 << 20, "2Gi": 2 << 30,
		"1G": 1_000_000_000, "500M": 500_000_000, "128974848": 128974848, "1.5Gi": 3 << 29, "129e6": 129_000_000,
	} {
		got, err := Bytes(written)
		if err != nil || got != want {
			t.Errorf("%q was read as %d (%v), not %d", written, got, err, want)
		}
	}
}
