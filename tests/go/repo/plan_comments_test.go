package repo

import (
	"regexp"
	"strings"
	"testing"
)

// Every comment this workflow posts replaces itself, and no plan lane creates
// a deployment record.
//
// Both are surfaces that said something untrue, and both are the shape that
// teaches a reader to stop looking at a surface - which costs far more than the
// thing being reported.

// A job's `environment:` line, and the job it belongs to.
var jobHeading = regexp.MustCompile(`(?m)^  ([a-z][a-z0-9-]*):\s*$`)

// A plan reads state and reports. It deploys nothing, so it must create no
// deployment record.
//
// Declaring `environment:` makes GitHub create one for the job, and a failed
// job leaves it failed permanently: nothing marks one succeeded, inactive or
// superseded, so they accumulate (#158). One pull request showed three "had a
// problem deploying to management" entries followed immediately by "This branch
// was successfully deployed" and "No deployments" - GitHub's own statements,
// contradicting each other on one page.
//
// This asserts only the part that is right whatever is decided about the
// converge job: a lane that has never deployed anything should not be leaving
// records that say it did.
func TestNoPlanLaneCreatesADeploymentRecord(t *testing.T) {
	body := intendedWorkflow(t, "deploy-infrastructure.yml")

	jobs := splitJobs(body)
	if len(jobs) < 4 {
		t.Fatalf(`found %d job(s) in deploy-infrastructure.yml, and it has more.

The parse has stopped matching, so every job it no longer sees is one this
check silently stopped reading.`, len(jobs))
	}

	checked := 0
	for name, block := range jobs {
		if !strings.Contains(name, "plan") {
			continue
		}
		checked++
		if regexp.MustCompile(`(?m)^\s+environment:\s*\S`).MatchString(block) {
			t.Errorf(`the job %q declares an environment.

It is a plan. It reads state, posts a comment, and changes nothing - so every
deployment record it creates describes something that did not happen, and a
failed run leaves one saying the management environment last failed to deploy,
indefinitely.

A status surface nobody maintains trains people to skim past it, and the next
time it is genuinely red it will look the same as it does now.`, name)
		}
	}
	if checked == 0 {
		t.Fatal("no plan job was found, so this test read nothing")
	}
}

// Every comment the workflow posts is found and replaced, not appended.
//
// Two of the three paths already did this. The workload plan's did not: it
// called createComment unconditionally, so every `synchronize` added another
// and the oldest sat at the top (#160). Nobody had seen it because the job
// gates on `environments/` and `modules/`, which did not exist when it was
// written - so it would have started spamming on the first pull request of
// epoch 02, which is exactly when nobody would remember it was known.
//
// Asserted as a class rather than as "the workload plan has a marker", because
// the failure is not that one path was missed. It is that a new comment path
// looks complete without one, and the next one added would go the same way.
func TestEveryCommentPathReplacesItselfRatherThanAppending(t *testing.T) {
	body := intendedWorkflow(t, "deploy-infrastructure.yml")

	// Each github-script block that posts a comment.
	creates := regexp.MustCompile(`issues\.createComment`).FindAllStringIndex(body, -1)
	if len(creates) < 3 {
		t.Fatalf(`found %d createComment call(s), and this workflow has three.

The parse has stopped matching, so a path that appends could be added without
this noticing.`, len(creates))
	}

	for _, loc := range creates {
		// The script block this call sits in, taken back to the step that
		// opened it. A marker and a lookup have to appear before the post.
		start := strings.LastIndex(body[:loc[0]], "- name:")
		if start < 0 {
			start = 0
		}
		block := body[start:loc[1]]
		line := 1 + strings.Count(body[:loc[0]], "\n")

		// The PREDICATE, not the word "marker". Looking for that word matches
		// the prose explaining the mechanism as readily as the mechanism, which
		// is the change-detector shape this repository refuses by name - and
		// the mutation ledger caught this check in exactly that state.
		if !strings.Contains(block, "listComments") {
			t.Errorf(`deploy-infrastructure.yml:%d posts a comment without looking for an
earlier one.

There is then nothing to find it by on the next push, so every run adds
another. That is noise of the specific kind that makes a reviewer stop reading
the comments - on the lane that comments about changes to real infrastructure.`, line)
			continue
		}
		if !strings.Contains(block, "body.startsWith(") {
			t.Errorf(`deploy-infrastructure.yml:%d lists the comments and matches none of
them against a marker.

Reading the comments and then posting regardless is the same appending
behaviour with an extra API call in front of it.`, line)
		}
		if !strings.Contains(block, "deleteComment") {
			t.Errorf(`deploy-infrastructure.yml:%d finds the previous comment and does not
remove it.

Editing in place is the other half of this and was rejected for a stated
reason (#246): an edited comment keeps the position it was first posted at
while its contents change underneath, so it ends up sitting beside a push that
no longer produced it.`, line)
		}
	}
}

// splitJobs returns each top-level job's block, keyed by name.
func splitJobs(body string) map[string]string {
	headings := jobHeading.FindAllStringSubmatchIndex(body, -1)
	jobs := map[string]string{}

	// Only what follows the `jobs:` key; the top-level `on:` and `env:` blocks
	// have the same shape at a different depth.
	jobsAt := strings.Index(body, "\njobs:")
	for i, h := range headings {
		if h[0] < jobsAt {
			continue
		}
		end := len(body)
		if i+1 < len(headings) {
			end = headings[i+1][0]
		}
		jobs[body[h[2]:h[3]]] = body[h[0]:end]
	}
	return jobs
}
