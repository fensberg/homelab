package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const marker = "<!-- sensitive-attestation:abc123 -->"

// No acknowledgement exists for this content, so one is opened - and opening
// it is what blocks the merge, through GitHub's conversation-resolution rule.
//
// This must NOT also refuse. Resolving a conversation emits no event a workflow
// can trigger on, so a check that went red here could never be turned green
// again: the reviewer would resolve the thread, nothing would re-run, and the
// merge would stay blocked forever. A gate that cannot open is a deadlock.
func TestNoThreadOpensOneAndDoesNotDeadlock(t *testing.T) {
	v := Decide(nil, marker, "author")
	if !v.OpenThread {
		t.Error("no conversation was opened, so nothing tells the reviewer what to do")
	}
	if v.Blocked {
		t.Error("the check refused as well as opening the conversation. Nothing re-runs " +
			"it when the thread is resolved, so this would be red forever.")
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
	if !v.OpenThread {
		t.Fatalf("a resolved acknowledgement of different content satisfied this one, so "+
			"no new conversation was opened for the change actually being made: %+v", v)
	}
}

// An open conversation is GitHub's to block, not this check's - and for the
// same reason as above: nothing would re-run this once it was resolved.
func TestAnOpenThreadIsLeftToGitHub(t *testing.T) {
	threads := []Thread{{FirstCommentBody: marker, Resolved: false}}
	v := Decide(threads, marker, "author")
	if v.Blocked {
		t.Error("the check refused an open conversation. GitHub already blocks the merge " +
			"while one is open, and nothing re-runs this when it is resolved - so this " +
			"would be red forever.")
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
	if v := Decide(threads, "", "author"); !v.OpenThread {
		t.Fatalf("an empty marker matched an unrelated thread, so a digest that failed to "+
			"compute would be satisfied by whatever conversation happened to exist: %+v", v)
	}
}

// A superseded conversation nobody answered is withdrawn; one that was
// answered stays.
//
// THE SITUATION THIS ENDS (#224). #223 carried two unresolved attestation
// conversations, both anchored to the same file, both saying exactly the same
// thing, differing only in their digest. Two pushes touched a sensitive path,
// so two conversations opened, and GitHub's require-conversation-resolution
// counted both. On a pull request with four pushes it is four.
//
// That recreates the failure sensitive-paths.yml was built to replace. The
// label it got rid of survived pushes, so people applied it without reading;
// faced with two threads carrying the same title, the same rule and the same
// file list, the second gets resolved without being read because it looks like
// the one just read.
//
// The digest binding is NOT weakened - a changed diff still opens a new
// conversation, which is what stops an acknowledgement outliving what it
// acknowledged. What changes is that the old one is withdrawn rather than left
// as an obligation, when and only when nobody answered it.
func TestSupersededConversationsNobodyAnsweredAreWithdrawn(t *testing.T) {
	const current = "<!-- sensitive-attestation:c59dbc1fa830 -->"

	threads := []Thread{
		{
			// An older digest, unresolved. Describes a diff that is gone.
			FirstCommentBody: "<!-- sensitive-attestation:21452458b457 -->\nread the change",
			FirstCommentID:   111,
		},
		{
			// An older digest that somebody DID read and resolve. That is the
			// record of an acknowledgement actually given.
			FirstCommentBody: "<!-- sensitive-attestation:aaaaaaaaaaaa -->\nread the change",
			FirstCommentID:   222,
			Resolved:         true,
			ResolvedBy:       "a-person",
			ResolvedByType:   "User",
		},
		{
			// Somebody else's review comment, nothing to do with this gate.
			FirstCommentBody: "this variable could be clearer",
			FirstCommentID:   333,
		},
		{
			FirstCommentBody: current + "\nread the change",
			FirstCommentID:   444,
		},
	}

	v := Decide(threads, current, "the-author")

	if len(v.DeleteComments) != 1 || v.DeleteComments[0] != 111 {
		t.Fatalf(`withdrew %v, want only the unanswered superseded thread (111).

  222 was resolved by a person - that is the record of an acknowledgement and
      destroying it would lose history.
  333 is somebody else's review comment and is none of this gate's business.
  444 is the live conversation, which is the one a human must read.`, v.DeleteComments)
	}
	if v.OpenThread {
		t.Error("a conversation exists for the current digest and another was opened anyway")
	}
	if v.Blocked {
		t.Errorf("the merge was blocked over superseded threads: %s", v.Reason)
	}
}

// Withdrawing happens even when there is no current conversation yet.
//
// That is the ordinary case on a push that changes the sensitive diff: the old
// thread is superseded in the same run that opens the new one. Handling only
// the case where both exist would leave every first-push-after-a-change
// carrying the old obligation.
func TestASupersededConversationIsWithdrawnWhenTheNewOneIsOpened(t *testing.T) {
	v := Decide([]Thread{
		{FirstCommentBody: "<!-- sensitive-attestation:oldoldoldold -->\nread it", FirstCommentID: 555},
	}, "<!-- sensitive-attestation:newnewnewnew -->", "the-author")

	if !v.OpenThread {
		t.Error("no conversation exists for the current digest and none was opened")
	}
	if len(v.DeleteComments) != 1 || v.DeleteComments[0] != 555 {
		t.Errorf("withdrew %v, want the superseded thread 555", v.DeleteComments)
	}
}

// Nothing is withdrawn when nothing is superseded.
//
// The converse, because a bug that withdrew the LIVE conversation would look
// exactly like this working: the gate would pass, the page would look tidy, and
// nobody would ever be asked to read anything.
func TestTheLiveConversationIsNeverWithdrawn(t *testing.T) {
	const current = "<!-- sensitive-attestation:c59dbc1fa830 -->"
	v := Decide([]Thread{
		{FirstCommentBody: current + "\nread it", FirstCommentID: 777},
	}, current, "the-author")

	if len(v.DeleteComments) != 0 {
		t.Fatalf(`withdrew %v with nothing superseded.

If the live conversation can be withdrawn, the gate passes with nobody having
read anything - which is the whole thing it exists to prevent.`, v.DeleteComments)
	}
}

// The withdrawal actually reaches GitHub, as a DELETE on the right comment.
//
// Decide says WHICH conversations to withdraw; this is the half that does it,
// and the two can disagree silently. GitHub has no mutation for deleting a
// review THREAD, so the thread is taken away by deleting the comment it hangs
// off - which means a wrong id deletes somebody's review comment instead.
func TestWithdrawingAConversationDeletesItsFirstComment(t *testing.T) {
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	saved := apiBase
	apiBase = srv.URL
	defer func() { apiBase = saved }()

	c := &client{repo: "example/homelab", token: "t", http: srv.Client()}
	if err := c.deleteComment(111); err != nil {
		t.Fatalf("deleteComment: %v", err)
	}
	if method != http.MethodDelete {
		t.Errorf("used %s; deleting a review comment is a DELETE", method)
	}
	if path != "/repos/example/homelab/pulls/comments/111" {
		t.Errorf("asked for %q, which is not the review comment it was given", path)
	}
}

// A refusal is reported and carries no response body.
//
// The body can echo the request, and this output lands in a public Actions
// log - the same reason post() does not quote one.
func TestAFailedWithdrawalIsReportedWithoutTheBody(t *testing.T) {
	const secretish = "ghs_do_not_print_me"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"` + secretish + `"}`))
	}))
	defer srv.Close()

	saved := apiBase
	apiBase = srv.URL
	defer func() { apiBase = saved }()

	c := &client{repo: "example/homelab", token: "t", http: srv.Client()}
	err := c.deleteComment(222)
	if err == nil {
		t.Fatal("a refused delete reported success, so a superseded conversation " +
			"would be believed withdrawn while still blocking the merge")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("the error does not name the status: %v", err)
	}
	if strings.Contains(err.Error(), secretish) {
		t.Errorf("the response body reached the error, and this lands in a public log: %v", err)
	}
}
