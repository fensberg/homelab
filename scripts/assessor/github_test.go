package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An answer is asked for once and read from the cache after; a 404 or a
// 410 is gone, and any other refusal is an error.
func TestAnAnswerIsAskedForOnceAndARefusalIsSaid(t *testing.T) {
	asked := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked[r.URL.Path]++
		switch r.URL.Path {
		case "/repos/o/r/kept":
			fmt.Fprint(w, "the answer")
		case "/repos/o/r/expired":
			w.WriteHeader(http.StatusGone)
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()
	c := &client{base: srv.URL, repo: "o/r", token: "tok", cache: t.TempDir()}

	for range 2 {
		if body, err := c.get("/kept"); err != nil || string(body) != "the answer" {
			t.Fatalf("got %q, %v", body, err)
		}
	}
	if asked["/repos/o/r/kept"] != 1 {
		t.Errorf("asked %d times; the second should have come from the cache", asked["/repos/o/r/kept"])
	}
	if _, err := c.get("/expired"); !errors.Is(err, errGone) {
		t.Errorf("a 410 should be gone, got %v", err)
	}
	if _, err := c.get("/forbidden"); err == nil || errors.Is(err, errGone) {
		t.Errorf("a 403 should be an error that is not gone, got %v", err)
	}
	broken := &client{base: "http://127.0.0.1:1", repo: "o/r", cache: t.TempDir()}
	if _, err := broken.get("/kept"); err == nil {
		t.Error("an unreachable API should be an error")
	}
	unwritable := &client{base: srv.URL, repo: "o/r", cache: "/nonexistent/cache"}
	if _, err := unwritable.get("/kept"); err == nil {
		t.Error("an answer that cannot be kept should be an error")
	}
}

// A listing is read page by page until a page comes back short, wrapped or
// not, and a page that is not the listing is an error.
func TestAListingIsReadToItsLastPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := 100
		if r.URL.Query().Get("page") == "2" {
			count = 3
		}
		items := strings.TrimSuffix(strings.Repeat(`{"number":1},`, count), ",")
		switch r.URL.Path {
		case "/repos/o/r/bare":
			fmt.Fprintf(w, "[%s]", items)
		case "/repos/o/r/wrapped":
			fmt.Fprintf(w, `{"total_count":103,"items":[%s]}`, items)
		case "/repos/o/r/cut-short":
			fmt.Fprintf(w, `{"total_count":5000,"items":[%s]}`, items)
		default:
			fmt.Fprint(w, "not json")
		}
	}))
	defer srv.Close()
	c := &client{base: srv.URL, repo: "o/r", cache: t.TempDir()}
	for path, field := range map[string]string{"/bare": "", "/wrapped?state=all": "items"} {
		got, err := pages[pull](c, path, field)
		if err != nil || len(got) != 103 {
			t.Errorf("%s: got %d items, %v; want 103", path, len(got), err)
		}
	}
	if _, err := pages[pull](c, "/cut-short", "items"); err == nil || !strings.Contains(err.Error(), "103 of the 5000") {
		t.Errorf("a listing GitHub stopped early should say so, got %v", err)
	}
	if _, err := pages[pull](c, "/broken", ""); err == nil {
		t.Error("a page that is not a listing should be an error")
	}
	if _, err := pages[pull](&client{base: "http://127.0.0.1:1", repo: "o/r", cache: t.TempDir()}, "/bare", ""); err == nil {
		t.Error("an unreachable API should be an error")
	}
}

// Work over many items reports the first error, and does all of it.
func TestEachDoesAllTheWorkAndReportsAnError(t *testing.T) {
	done := make(chan int, 20)
	err := each(make([]int, 20), func(int) error {
		done <- 1
		return nil
	})
	if err != nil || len(done) != 20 {
		t.Errorf("got %v after %d of 20", err, len(done))
	}
	if err := each([]int{1, 2, 3}, func(n int) error {
		if n == 2 {
			return errors.New("two")
		}
		return nil
	}); err == nil || err.Error() != "two" {
		t.Errorf("got %v, want the error", err)
	}
}
