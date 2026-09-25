package phases

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A fake Access API: pages of applications, and an optional status to fail
// with.
func accessAPI(t *testing.T, status int, pages ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("the request carried no bearer token: %q", r.Header.Get("Authorization"))
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"success":false,"errors":[{"message":"no"}]}`)
			return
		}
		page := 1
		fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
		fmt.Fprintf(w, `{"success":true,"result":[%s],"result_info":{"total_pages":%d}}`, pages[page-1], len(pages))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTheEnrollmentApplicationIsFoundByType(t *testing.T) {
	srv := accessAPI(t, http.StatusOK,
		`{"id":"a1","type":"self_hosted"},{"id":"a2","type":"bookmark"}`,
		`{"id":"w1","type":"warp"}`)
	id, err := findEnrollmentApp(srv.Client(), srv.URL, "tok")
	if err != nil || id != "w1" {
		t.Errorf("want the warp application on the second page, got %q, %v", id, err)
	}
}

// None at all - as after the demolish that deleted it - is "create it", not
// an error.
func TestAMissingEnrollmentApplicationIsLeftForTheApplyToCreate(t *testing.T) {
	srv := accessAPI(t, http.StatusOK, `{"id":"a1","type":"self_hosted"}`)
	id, err := findEnrollmentApp(srv.Client(), srv.URL, "tok")
	if err != nil || id != "" {
		t.Errorf("want no id and no error, got %q, %v", id, err)
	}
}

// Not knowing must never read as "there is none": the apply would try to
// create a second application and Cloudflare would refuse it mid-run.
func TestAnUnreadableAnswerIsAnErrorNotAbsence(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusInternalServerError} {
		srv := accessAPI(t, status)
		id, err := findEnrollmentApp(srv.Client(), srv.URL, "tok")
		if err == nil || id != "" {
			t.Errorf("HTTP %d read as %q with no error", status, id)
		}
		if err != nil && !strings.Contains(err.Error(), fmt.Sprint(status)) {
			t.Errorf("HTTP %d: the error does not say what came back: %v", status, err)
		}
	}
}
