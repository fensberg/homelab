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
//
// The rule is about machines rather than about the author. A human closing a
// conversation on their own pull request is already covered elsewhere: GitHub
// forbids approving your own pull request and the ruleset requires one
// approval, so a human-authored change needs a second human before it can
// merge at all. A machine closing one is what no other rule would notice.
func TestResolvedByABotIsRefused(t *testing.T) {
	for _, closer := range []Thread{
		{FirstCommentBody: marker, Resolved: true, ResolvedBy: "github-actions[bot]"},
		{FirstCommentBody: marker, Resolved: true, ResolvedBy: "fensberg-claude[bot]"},
		{FirstCommentBody: marker, Resolved: true, ResolvedBy: "anything", ResolvedByType: "Bot"},
	} {
		v := Decide([]Thread{closer}, marker, "someone")
		if !v.Blocked {
			t.Errorf("%s closed the acknowledgement and the merge was allowed", closer.ResolvedBy)
		}
		if !strings.Contains(v.Reason, "human act") {
			t.Errorf("the refusal does not say why a machine cannot acknowledge:\n%s", v.Reason)
		}
	}
}

// Both signals, not either. GraphQL's __typename is authoritative and the
// login suffix is the fallback for when it is absent, because the consequence
// of reading a bot as a human is that the gate passes.
func TestABotIsDetectedByEitherSignal(t *testing.T) {
	if !isMachine("x", "Bot") {
		t.Error("a Bot typename was read as human")
	}
	if !isMachine("dependabot[bot]", "") {
		t.Error("a [bot] login with no typename was read as human")
	}
	if isMachine("jlemberg", "User") {
		t.Error("a person was read as a machine, which would block every acknowledgement")
	}
	if isMachine("[bot]", "") {
		t.Error("a login that is only the suffix was treated as a bot name")
	}
}

// The author closing their own conversation is allowed, deliberately. Opening
// one by accident and closing it again must not lock somebody out of their own
// pull request, and the approval requirement already means a human-authored
// change needs a second human.
func TestTheAuthorMayCloseTheirOwnConversation(t *testing.T) {
	threads := []Thread{{
		FirstCommentBody: marker, Resolved: true,
		ResolvedBy: "jlemberg", ResolvedByType: "User",
	}}
	if v := Decide(threads, marker, "jlemberg"); v.Blocked {
		t.Fatalf("a person was blocked from closing a conversation on their own pull "+
			"request: %s", v.Reason)
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
	threads := []Thread{{FirstCommentBody: marker, Resolved: true, ResolvedBy: "reviewer", ResolvedByType: "User"}}
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
