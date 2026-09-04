package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The key is the thing that must never escape.
//
// This program runs in a public repository's Actions log. A key in a URL is a
// key in a log line, a redirect, and any error that quotes the request - so it
// travels in a header, and every assertion below that looks for it is checking
// a place it has actually leaked from before in other projects.
const testKey = "AIzaSyTESTKEYVALUEDONOTUSE"

func testAsker(t *testing.T, h http.HandlerFunc) (*asker, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &asker{
		endpoint: srv.URL,
		model:    "test-model",
		key:      testKey,
		http:     srv.Client(),
		attempts: 3,
		backoff:  time.Millisecond,
	}, srv
}

func ok(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

const oneCandidate = `{"candidates":[{"content":{"parts":[{"text":"the account"}]}}]}`

func TestAskReturnsTheText(t *testing.T) {
	a, _ := testAsker(t, ok(oneCandidate))

	got, err := a.ask("describe this")
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if got != "the account" {
		t.Errorf("got %q, want %q", got, "the account")
	}
}

// The key goes in a header, never in the query string.
//
// Google's API accepts both. The query form is the one every example uses and
// the one that ends up in a log, a proxy trace and a Referer header, so this
// asserts the choice rather than trusting whoever edits ask() next.
func TestAskSendsTheKeyAsAHeaderAndNotInTheURL(t *testing.T) {
	var sawHeader, sawURL string
	a, _ := testAsker(t, func(w http.ResponseWriter, r *http.Request) {
		sawHeader = r.Header.Get("x-goog-api-key")
		sawURL = r.URL.String()
		_, _ = w.Write([]byte(oneCandidate))
	})

	if _, err := a.ask("q"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	if sawHeader != testKey {
		t.Errorf("key not sent as x-goog-api-key header, got %q", sawHeader)
	}
	if strings.Contains(sawURL, testKey) {
		t.Errorf("key appears in the request URL: %s", sawURL)
	}
}

// An error is read by a human in a world-readable log.
//
// Every failure path is checked, not just one, because the leak only has to
// happen on the path nobody thought about.
func TestNoErrorEverQuotesTheKey(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"refused", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "API key not valid: "+testKey, http.StatusForbidden)
		}},
		{"rate limited", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "quota exceeded for "+testKey, http.StatusTooManyRequests)
		}},
		{"unparseable", ok(`{"candidates":`)},
		{"no candidates", ok(`{"candidates":[]}`)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, _ := testAsker(t, c.handler)
			_, err := a.ask("q")
			if err == nil {
				t.Fatal("expected an error")
			}
			if strings.Contains(err.Error(), testKey) {
				t.Errorf("error quotes the key: %v", err)
			}
		})
	}
}

// A rate limit is retried, and the retries are bounded.
//
// Bounded because the building code says so: a recovery that fails must not
// loop. Three attempts, then the error names how many were made, because
// "it failed" and "it failed three times over ninety seconds" are different
// facts to the person reading the log.
func TestRateLimitIsRetriedAndBounded(t *testing.T) {
	var calls int
	a, _ := testAsker(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "quota exceeded", http.StatusTooManyRequests)
	})

	_, err := a.ask("q")
	if err == nil {
		t.Fatal("expected an error after exhausting attempts")
	}
	if calls != a.attempts {
		t.Errorf("made %d attempts, want %d", calls, a.attempts)
	}
}

func TestRateLimitThatClearsSucceeds(t *testing.T) {
	var calls int
	a, _ := testAsker(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 2 {
			http.Error(w, "quota exceeded", http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(oneCandidate))
	})

	got, err := a.ask("q")
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if got != "the account" {
		t.Errorf("got %q", got)
	}
}

// A refusal is not retried.
//
// The lesson from #197, applied here rather than only written down: an error
// that cannot become true by waiting must not be waited on. A bad key is a bad
// key on the third attempt, and retrying it burns a metered runner and buries
// the cause under a timeout that names something else.
func TestARefusalIsNotRetried(t *testing.T) {
	var calls int
	a, _ := testAsker(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "API key not valid", http.StatusForbidden)
	})

	if _, err := a.ask("q"); err == nil {
		t.Fatal("expected an error")
	}
	if calls != 1 {
		t.Errorf("retried a refusal %d times; it cannot become true by waiting", calls)
	}
}

// Silence must not read as a clean answer.
//
// A response with no candidates is the model declining, or a safety filter, or
// a shape this code does not understand. All three are "I did not get an
// account", and none of them is an empty account - which would otherwise be
// reported as a finding-free run.
func TestAnEmptyAnswerIsAnError(t *testing.T) {
	a, _ := testAsker(t, ok(`{"candidates":[]}`))

	got, err := a.ask("q")
	if err == nil {
		t.Fatalf("no candidates returned %q and no error", got)
	}
	if !errors.Is(err, errNoAnswer) {
		t.Errorf("want errNoAnswer, got %v", err)
	}
}
