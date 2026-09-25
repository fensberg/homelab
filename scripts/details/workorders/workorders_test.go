package workorders

import (
	"os"
	"testing"
)

func TestTheRepositorysWorkOrdersRead(t *testing.T) {
	body, err := os.ReadFile("../../../" + Path)
	if err != nil {
		t.Fatal(err)
	}
	orders, err := Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	releases := 0
	for _, o := range orders {
		if o.Name == "" || o.Context == "" {
			t.Errorf("an order reads with no name or context: %+v", o)
		}
		if o.Release != nil {
			releases++
		}
	}
	if releases == 0 {
		t.Error("no order reads as making a release, so production would have nothing to pin")
	}
	if _, err := Parse([]byte(`{"orders":[]}`)); err == nil {
		t.Error("an empty list of orders was accepted")
	}
}
