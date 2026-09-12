package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func post(title, feed string, at time.Time) item {
	return item{Gid: title, Title: title, Feedname: feed, Date: at.Unix()}
}

// The doorbell rings for Valve and for nobody else.
//
// Each row is a way this could ring wrongly, and the two that matter are the
// press article - the endpoint returns those beside Valve's own posts, and one
// of them is a 2021 piece about a boat mod - and the old announcement, which is
// what every run would see forever if age were not checked.
func TestRecentRingsOnlyForAFreshPostFromValve(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name  string
		items []item
		want  string // the title expected to ring, or "" for silence
	}{
		{
			name:  "a hotfix posted twenty minutes ago",
			items: []item{post("Hotfix 1.0.12", valveFeed, now.Add(-20*time.Minute))},
			want:  "Hotfix 1.0.12",
		},
		{
			name:  "nothing at all",
			items: nil,
		},
		{
			name:  "a press article, posted now",
			items: []item{post("This Valheim mod lets you build big boats", "eurogamer", now)},
		},
		{
			name:  "Valve's own post, but from last year",
			items: []item{post("Patch 0.218.15", valveFeed, now.AddDate(-1, 0, 0))},
		},
		{
			name: "a fresh press article beside a stale announcement",
			items: []item{
				post("Valheim 1.0 is finally here", "GamingOnLinux", now.Add(-time.Minute)),
				post("Patch 0.217.46", valveFeed, now.AddDate(0, -2, 0)),
			},
		},
		{
			name: "the newest of several fresh posts",
			items: []item{
				post("Hotfix 1.0.10", valveFeed, now.Add(-80*time.Minute)),
				post("Hotfix 1.0.12", valveFeed, now.Add(-10*time.Minute)),
			},
			want: "Hotfix 1.0.12",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, found := Recent(c.items, valveFeed, now, 90*time.Minute)
			if c.want == "" {
				if found {
					t.Fatalf("rang for %q, and should have stayed silent", got.Title)
				}
				return
			}
			if !found {
				t.Fatal("stayed silent, and should have rung")
			}
			if got.Title != c.want {
				t.Errorf("rang for %q, want %q", got.Title, c.want)
			}
		})
	}
}

// The window's edge, both sides of it.
//
// A post exactly at the edge must still ring: the alternative is a post made in
// the same second as the previous check falling into the gap between two runs,
// which is the one failure this design has and the reason the window is longer
// than the interval.
func TestRecentIncludesTheEdgeOfTheWindow(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	within := 90 * time.Minute

	just := []item{post("inside", valveFeed, now.Add(-within).Add(time.Second))}
	if _, found := Recent(just, valveFeed, now, within); !found {
		t.Error("a post one second inside the window did not ring")
	}

	past := []item{post("outside", valveFeed, now.Add(-within).Add(-time.Second))}
	if _, found := Recent(past, valveFeed, now, within); found {
		t.Error("a post one second outside the window rang")
	}
}

// Steam's own shape, parsed from the wire.
//
// The fixture is the response's real shape - appnews.newsitems, with date as a
// Unix integer - so a change in what this program expects fails here rather
// than at four in the morning.
func TestFetchNewsReadsSteamsShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"appnews":{"appid":892970,"newsitems":[
			{"gid":"1","title":"Hotfix 1.0.10 & 1.0.12","feedname":"steam_community_announcements","date":1789131742},
			{"gid":"2","title":"An article","feedname":"GamingOnLinux","date":1788961579}],"count":2}}`)
	}))
	defer srv.Close()

	got, err := fetchNews(srv.URL)
	if err != nil {
		t.Fatalf("reading Steam's shape: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d posts, want 2", len(got))
	}
	if got[0].Title != "Hotfix 1.0.10 & 1.0.12" || got[0].Feedname != valveFeed {
		t.Errorf("first post = %+v", got[0])
	}
	if got[0].Date <= got[1].Date {
		t.Errorf("posts are not newest first: %d then %d", got[0].Date, got[1].Date)
	}
}

// An unreachable supplier is not a quiet one.
//
// "Steam did not answer" and "Valve published nothing" mean opposite things,
// and only one of them means the estate is up to date. A check that swallowed
// the first would report a current server for as long as the outage lasted.
func TestCheckRefusesRatherThanReportingSilenceWhenSteamFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if rc := check([]string{"-url", srv.URL}); rc == 0 {
		t.Fatal("an HTTP 500 from Steam was reported as nothing to do")
	}

	if _, err := fetchNews("http://127.0.0.1:1/never"); err == nil {
		t.Error("an unreachable endpoint returned no error")
	}
}

// Nonsense on the wire is an error, not an empty list.
func TestFetchNewsRefusesAnAnswerItCannotRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "<html>maintenance</html>")
	}))
	defer srv.Close()

	_, err := fetchNews(srv.URL)
	if err == nil {
		t.Fatal("HTML was parsed as news")
	}
	if !strings.Contains(err.Error(), "not the shape") {
		t.Errorf("the error should say the answer was the wrong shape: %v", err)
	}
}

// The URL names the app and asks for the newest few.
func TestNewsURLAsksForOneApp(t *testing.T) {
	got := newsURL("892970", 10)
	for _, want := range []string{"appid=892970", "count=10", "ISteamNews/GetNewsForApp"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is missing %q", got, want)
		}
	}
}
