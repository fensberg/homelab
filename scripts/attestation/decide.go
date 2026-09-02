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

// Thread is one review conversation, reduced to what the decision needs.
type Thread struct {
	FirstCommentBody string
	Resolved         bool
	ResolvedBy       string
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
}

// Decide answers the question the workflow exists to ask.
//
// Four outcomes, and the last is the one that should never happen:
//
//   - no acknowledgement for this content - open one, block
//   - one exists and is open - block
//   - resolved, and by nobody GitHub will name - block, because "I cannot tell
//     who acknowledged this" and "somebody did" are different answers
//   - resolved by the author of the pull request - block, because approving
//     and acknowledging have to be two separate acts by two people
func Decide(threads []Thread, marker, prAuthor string) Verdict {
	var found *Thread
	for i := range threads {
		if contains(threads[i].FirstCommentBody, marker) {
			found = &threads[i]
			break
		}
	}

	if found == nil {
		return Verdict{
			OpenThread: true,
			Blocked:    true,
			Reason: "This change touches a sensitive path and has no acknowledgement. " +
				"A review conversation has been opened on the file - read the change, " +
				"then resolve it. Somebody other than the author has to.",
		}
	}

	if !found.Resolved {
		return Verdict{Blocked: true,
			Reason: "The review conversation acknowledging this change is still open. " +
				"Read the change and resolve it."}
	}

	if found.ResolvedBy == "" {
		return Verdict{Blocked: true,
			Reason: "The conversation is resolved but GitHub reports nobody as having " +
				"resolved it, so this cannot be shown to be a real acknowledgement."}
	}

	if found.ResolvedBy == prAuthor {
		return Verdict{Blocked: true, Reason: fmt.Sprintf(
			"The acknowledgement was closed by %s, who opened this pull request. "+
				"Approving and acknowledging have to be two separate acts by two "+
				"different people - otherwise the gate is a formality the author "+
				"performs on themselves. Reopen the conversation and have somebody "+
				"else read the change.", found.ResolvedBy)}
	}

	return Verdict{Reason: fmt.Sprintf("Acknowledged by %s.", found.ResolvedBy)}
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
