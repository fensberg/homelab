package tofustate

import (
	"strings"
	"testing"
)

func TestManagedLeavesOutDataSources(t *testing.T) {
	st, err := Parse([]byte(`{"version":4,"serial":3,"lineage":"l","resources":[
		{"mode":"data","type":"d","name":"a"},
		{"mode":"managed","type":"t","name":"b"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(st.Managed(), " "); got != "t.b" {
		t.Errorf("got %q, want t.b", got)
	}
	if st.Version != 4 || st.Serial != 3 || st.Lineage != "l" || len(st.Resources) != 2 {
		t.Errorf("got %+v", st)
	}
	if _, err := Parse([]byte("not json")); err == nil {
		t.Error("something that is not JSON parsed as state")
	}
}
