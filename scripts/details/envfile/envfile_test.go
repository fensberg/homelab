package envfile

import "testing"

func TestParseReadsAssignmentsAndSkipsTheRest(t *testing.T) {
	got := Parse("# a comment\n\nA_TOOL_VERSION=1.12.6\n  B_TOOL_VERSION = 1.26.7 \nnot an assignment\n# X=1\n")
	want := map[string]string{"A_TOOL_VERSION": "1.12.6", "B_TOOL_VERSION": "1.26.7"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
}
