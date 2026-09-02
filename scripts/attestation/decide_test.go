package main

import (
	"strings"
	"testing"
)

const marker = "<!-- sensitive-attestation:abc123 -->"

// No acknowledgement exists for this content.
//
// The request and the refusal are the same event on purpose. Opening a
// conversation that nobody has to answer is how a gate becomes decoration.
func TestNoThreadOpensOneAndBlocks(t *testing.T) {
	v := Decide(nil, marker, "author")
	if !v.OpenThread {
		t.Error("no conversation was opened, so nothing tells the reviewer what to do")
	}
	if !v.Blocked {
		t.Error("the merge was not blocked, so the conversation is decoration")
	}
}

// A thread for different content does not acknowledge this content. This is
// the whole reason the marker carries a digest: an acknowledgement must not
// outlive the diff it acknowledged.
func TestAThreadForOtherContentDoesNotCount(t *testing.T) {
	threads := []Thread{{
		FirstCommentBody: "<!-- sensitive-attestation:999999 -->\nsomething else",
		Resolved:         true, ResolvedBy: "reviewer",
	}}
	v := Decide(threads, marker, "author")
	if !v.OpenThread || !v.Blocked {
		t.Fatalf("a resolved acknowledgement of different content satisfied this one: %+v", v)
	}
}

func TestAnOpenThreadBlocks(t *testing.T) {
	threads := []Thread{{FirstCommentBody: marker, Resolved: false}}
	v := Decide(threads, marker, "author")
	if !v.Blocked {
		t.Error("an unresolved acknowledgement allowed the merge")
	}
	if v.OpenThread {
		t.Error("a second conversation would be opened for content that already has one")
	}
}

// The check that should never fire.
func TestResolvedByTheAuthorIsRefused(t *testing.T) {
	threads := []Thread{{FirstCommentBody: marker, Resolved: true, ResolvedBy: "author"}}
	v := Decide(threads, marker, "author")
	if !v.Blocked {
		t.Fatal("the author acknowledged their own change and the merge was allowed")
	}
	if !strings.Contains(v.Reason, "two separate acts") {
		t.Errorf("the refusal does not say why self-acknowledgement is not one:\n%s", v.Reason)
	}
}

// Resolved with no recorded actor. Refusing rather than guessing: "I cannot
// tell who acknowledged this" and "somebody did" are different answers, and
// only one of them is a gate.
func TestResolvedByNobodyIsRefused(t *testing.T) {
	threads := []Thread{{FirstCommentBody: marker, Resolved: true, ResolvedBy: ""}}
	if v := Decide(threads, marker, "author"); !v.Blocked {
		t.Fatal("a resolution GitHub attributes to nobody was accepted as an acknowledgement")
	}
}

func TestResolvedBySomebodyElsePasses(t *testing.T) {
	threads := []Thread{{FirstCommentBody: marker, Resolved: true, ResolvedBy: "reviewer"}}
	v := Decide(threads, marker, "author")
	if v.Blocked {
		t.Fatalf("a genuine acknowledgement was refused: %s", v.Reason)
	}
	if !strings.Contains(v.Reason, "reviewer") {
		t.Errorf("the pass does not record who acknowledged it: %s", v.Reason)
	}
}

// An empty marker must never match. A digest that failed to compute would
// otherwise match the first thread on the pull request and pass.
func TestAnEmptyMarkerMatchesNothing(t *testing.T) {
	threads := []Thread{{FirstCommentBody: "anything", Resolved: true, ResolvedBy: "reviewer"}}
	if v := Decide(threads, "", "author"); !v.Blocked || !v.OpenThread {
		t.Fatalf("an empty marker matched an unrelated thread: %+v", v)
	}
}
