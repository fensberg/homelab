// Package main decides whether a change touching a sensitive path has been
// acknowledged by somebody other than the person who wrote it.
//
// The decision lives in Go rather than inline in a workflow for the reason
// sensitive-paths.sh already gives about itself: a script is reviewable, is
// linted, and can be run against known input by a test. YAML embedded in a
// workflow is none of those - it is behaviour that exists only as a
// convention somebody remembered to write, and a convention fails.
package main

import "fmt"

// attestationMarkerPrefix identifies a conversation this program opened,
// whatever digest it carries. The full marker is this plus the digest and
// " -->".
const attestationMarkerPrefix = "<!-- sensitive-attestation:"

// Thread is one review conversation, reduced to what the decision needs.
type Thread struct {
	FirstCommentBody string
	Resolved         bool
	ResolvedBy       string
	// FirstCommentID is the REST id of the comment the thread hangs off.
	// Deleting that comment removes the thread, which is the only way to take
	// one away - GitHub has no mutation for deleting a review thread itself.
	FirstCommentID int64
	// ResolvedByType is GraphQL's __typename for the actor: "User" or "Bot".
	// Empty when GitHub did not say, which is treated as unknown rather than
	// as human.
	ResolvedByType string
}

// Verdict is what to do about a pull request touching a sensitive path.
type Verdict struct {
	// OpenThread is true when no acknowledgement exists for this content and
	// one must be opened. The request and the refusal are the same event:
	// opening a conversation nobody has to answer is how a gate becomes
	// decoration.
	OpenThread bool
	// Blocked is true when the merge must not proceed.
	Blocked bool
	Reason  string
	// DeleteComments are the first-comment ids of conversations that describe
	// content this pull request no longer carries and that nobody answered.
	//
	// WHY THESE GO. The digest binds an acknowledgement to content, so a push
	// that changes the sensitive part of the diff opens a NEW conversation and
	// leaves the old one behind. That much is correct and must not be weakened:
	// it is what stops an acknowledgement outliving what it acknowledged.
	//
	// But leaving one behind is not the same as requiring it to be resolved,
	// and GitHub's require-conversation-resolution counts both (#224). #223
	// carried two threads on the same file, saying the same thing, differing
	// only in their digest. Four pushes would have been four.
	//
	// That recreates exactly the failure sensitive-paths.yml replaced. The
	// label it got rid of survived pushes, so people applied it without
	// reading; faced with two threads carrying the same title, the same rule
	// and the same file list, the second gets resolved without being read,
	// because it looks like the one just read. And the worse version is
	// available: resolve only the SUPERSEDED one, read nothing current, and
	// believe the acknowledgement has been given.
	//
	// A superseded thread nobody resolved describes content that no longer
	// exists, so resolving it asserts nothing true and deleting it loses
	// nothing. A superseded thread that WAS resolved is the record of an
	// acknowledgement actually given, and stays.
	//
	// This is not a machine resolving an acknowledgement. Nothing is marked
	// answered; an unanswered flag about absent content is withdrawn, and a
	// human still resolves exactly one conversation - the live one. Arguably
	// it strengthens the rule, because there is then only one thread and it is
	// unambiguously the current one.
	DeleteComments []int64
}

// Decide answers the question the workflow exists to ask.
//
// What this may refuse is constrained by an absence in GitHub: resolving a
// conversation emits no event a workflow can trigger on. So a check that
// blocked on an open conversation could never be turned green again - there
// would be nothing to re-run it - and the gate would be a deadlock.
//
// GitHub's own "require conversation resolution before merging" already blocks
// the merge while a thread is open, immediately and with no workflow involved.
// That half needs nothing from here. What is left is the half GitHub will not
// do, and it is the reason this exists at all: refusing a resolution by a
// machine. Clearing that needs a push, which is the right cost for something
// that should never happen.
//
// Four outcomes, and the last is the one that should never happen:
//
//   - no acknowledgement for this content - open one, and pass
//   - one exists and is open - pass
//   - resolved, and by nobody GitHub will name - block, because "I cannot tell
//     who acknowledged this" and "somebody did" are different answers
//   - resolved by a bot - block, because acknowledging is a human act
//
// The rule is about machines rather than about the author, and that is the
// stronger version. A human closing a conversation on their own pull request
// is already covered: GitHub forbids approving your own pull request and the
// ruleset requires one approval, so a human-authored change needs a second
// human before it can merge at all. What that does not cover is a machine
// closing a conversation, which no other rule anywhere would notice.
//
// It is also the version that does not punish an honest mistake. Opening a
// conversation and closing it again should not lock somebody out of their own
// pull request.
func Decide(threads []Thread, marker, prAuthor string) Verdict {
	var found *Thread
	var superseded []int64
	for i := range threads {
		switch {
		case contains(threads[i].FirstCommentBody, marker):
			if found == nil {
				found = &threads[i]
			}
		case contains(threads[i].FirstCommentBody, attestationMarkerPrefix):
			// One of ours, for a digest this pull request no longer carries.
			if !threads[i].Resolved && threads[i].FirstCommentID != 0 {
				superseded = append(superseded, threads[i].FirstCommentID)
			}
		}
	}

	if found == nil {
		// Opened, not refused. The conversation itself is what blocks the
		// merge, through GitHub's resolution rule - and refusing here as well
		// would leave a red check that no event could ever turn green.
		return Verdict{
			OpenThread:     true,
			DeleteComments: superseded,
			Reason: "This change touches a sensitive path. A review conversation has been " +
				"opened on the file: read the change, then resolve it. The merge is " +
				"blocked until somebody does.",
		}
	}

	if !found.Resolved {
		return Verdict{DeleteComments: superseded,
			Reason: "The review conversation acknowledging this change is " +
				"still open, and the merge is blocked until it is resolved."}
	}

	if found.ResolvedBy == "" {
		return Verdict{Blocked: true, DeleteComments: superseded,
			Reason: "The conversation is resolved but GitHub reports nobody as having " +
				"resolved it, so this cannot be shown to be a real acknowledgement."}
	}

	if isMachine(found.ResolvedBy, found.ResolvedByType) {
		return Verdict{Blocked: true, DeleteComments: superseded, Reason: fmt.Sprintf(
			"The acknowledgement was closed by %s, which is a machine. Reading a "+
				"change and deciding it is safe is a human act - a bot closing this "+
				"conversation has acknowledged nothing, it has only made the page look "+
				"like somebody did. Reopen it and have a person read the change.",
			found.ResolvedBy)}
	}
	_ = prAuthor

	return Verdict{DeleteComments: superseded,
		Reason: fmt.Sprintf("Acknowledged by %s.", found.ResolvedBy)}
}

// isMachine reports whether an actor is a bot.
//
// GraphQL's __typename is the authoritative answer and the login suffix is the
// fallback for when it is absent - both, rather than either, because the
// consequence of reading a bot as a human is that the gate passes.
func isMachine(login, typename string) bool {
	if typename == "Bot" {
		return true
	}
	const suffix = "[bot]"
	return len(login) > len(suffix) && login[len(login)-len(suffix):] == suffix
}

// contains is strings.Contains, spelled out so this file has no imports beyond
// fmt and stays trivially readable - it is the file somebody reads when they
// want to know what the gate actually does.
func contains(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
