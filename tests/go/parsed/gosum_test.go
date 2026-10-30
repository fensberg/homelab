package parsed

import "testing"

func TestGoSumOfReadsWhatGoWritesAndRefusesTheRest(t *testing.T) {
	lines, err := GoSumOf("example.com/a v1.2.3 h1:abc+/=\nexample.com/a v1.2.3/go.mod h1:def=\n")
	if err != nil || len(lines) != 2 || lines[1].Version != "v1.2.3/go.mod" {
		t.Fatalf("read %+v, %v", lines, err)
	}
	for _, bad := range []string{"# a comment\n", "example.com/a 1.2.3 h1:abc=\n", "example.com/a v1.2.3 sha256:abc\n"} {
		if _, err := GoSumOf(bad); err == nil {
			t.Errorf("%q was read as a go.sum", bad)
		}
	}
}
