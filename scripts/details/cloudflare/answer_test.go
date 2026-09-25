package cloudflare

import (
	"encoding/json"
	"testing"
)

func TestTheEnvelopeDecodesAResultAndItsPaging(t *testing.T) {
	var a Answer[[]struct {
		ID string `json:"id"`
	}]
	body := `{"success":true,"result":[{"id":"x"}],"result_info":{"total_pages":3}}`
	if err := json.Unmarshal([]byte(body), &a); err != nil {
		t.Fatal(err)
	}
	if !a.Success || len(a.Result) != 1 || a.Result[0].ID != "x" || a.ResultInfo.TotalPages != 3 {
		t.Errorf("decoded %+v", a)
	}
}
