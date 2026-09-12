package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// An announcement as Steam's news API returns it.
//
// Only the four fields that decide anything are read. The body is deliberately
// not: whether Valve published is a question about the existence and age of a
// post, and reading its prose would invite guessing at what the post means.
type item struct {
	Gid      string `json:"gid"`
	Title    string `json:"title"`
	Feedname string `json:"feedname"`
	Date     int64  `json:"date"`
}

type newsResponse struct {
	AppNews struct {
		Items []item `json:"newsitems"`
	} `json:"appnews"`
}

// valveFeed is the feed Valve posts to itself.
//
// The same endpoint also returns press articles - GamingOnLinux, Eurogamer,
// a Russian gaming site - which say nothing about whether a build shipped. One
// of those carried a 2021 article about a boat mod, which is exactly the kind
// of thing a doorbell must not ring for.
const valveFeed = "steam_community_announcements"

// newsURL asks for the newest few posts. No API key: this endpoint is public,
// and a key would be one more secret for a question anybody may ask.
func newsURL(appID string, count int) string {
	return fmt.Sprintf(
		"https://api.steampowered.com/ISteamNews/GetNewsForApp/v2/?appid=%s&count=%d&maxlength=1",
		appID, count)
}

// fetchNews returns the posts, newest first.
func fetchNews(url string) ([]item, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Steam answered %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var parsed newsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("Steam's answer is not the shape this expects: %w", err)
	}
	out := parsed.AppNews.Items
	sort.SliceStable(out, func(i, j int) bool { return out[i].Date > out[j].Date })
	return out, nil
}

// Recent reports the newest post Valve itself made inside the window.
//
// Window rather than "since the last one I saw", deliberately. Remembering the
// last post seen means state - a file to write, a race to lose, a value that
// drifts out of step with the pin beside it - to save a SteamCMD run that
// costs two minutes and answers the question properly anyway. A window has no
// state at all: it is true for as long as the news is fresh, the workflow looks
// properly, and a duplicate look costs one cheap run that says "nothing to do".
//
// So the window must be longer than the gap between checks, or a post made just
// after one check goes unseen by the next.
func Recent(items []item, feed string, now time.Time, within time.Duration) (item, bool) {
	// The newest match, not the first one seen. fetchNews sorts before
	// returning, but a function that is only correct when its caller sorted
	// first has an input it does not state - and the post this picks is the
	// one that gets logged as the reason a delivery was taken.
	cutoff := now.Add(-within)
	var newest item
	var found bool
	for _, i := range items {
		if i.Feedname != feed {
			continue
		}
		if !time.Unix(i.Date, 0).UTC().After(cutoff) {
			continue
		}
		if !found || i.Date > newest.Date {
			newest, found = i, true
		}
	}
	return newest, found
}

func check(args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	// The CLIENT appid, not the server's. Valve posts patch notes against the
	// game; the dedicated server appid's feed carries press articles and
	// nothing from Valve at all.
	appID := fs.String("appid", "892970", "the Steam appid whose announcements to read")
	feed := fs.String("feed", valveFeed, "the feed Valve itself posts to")
	within := fs.Duration("within", 90*time.Minute,
		"how fresh a post must be to be worth looking properly; longer than the gap between checks")
	count := fs.Int("count", 10, "how many posts to read")
	url := fs.String("url", "", "where to ask (default: Steam's news API for -appid)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ask := *url
	if ask == "" {
		ask = newsURL(*appID, *count)
	}

	items, err := fetchNews(ask)
	if err != nil {
		// Loud, and not "nothing to do". An unreachable supplier and a supplier
		// with nothing new look identical in an exit code, and only one of them
		// means the estate is up to date.
		fmt.Fprintf(os.Stderr, "expediter check: could not ask Steam whether anything was published: %v\n", err)
		return 1
	}

	post, found := Recent(items, *feed, time.Now().UTC(), *within)
	if !found {
		fmt.Println("idle")
		fmt.Fprintf(os.Stderr, "no post from Valve in the last %s (%d posts read)\n", *within, len(items))
		return 0
	}

	fmt.Println("look")
	fmt.Fprintf(os.Stderr, "Valve posted %q at %s - worth asking SteamCMD what the build is\n",
		strings.TrimSpace(post.Title), time.Unix(post.Date, 0).UTC().Format(time.RFC3339))
	return 0
}
