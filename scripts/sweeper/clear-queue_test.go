package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Only a run that can never produce a wanted result is cancelled.
//
// Two plans on a merged, deleted branch sat queued until an ignition refused
// to start because of them - at the one moment when being stopped is most
// expensive (#181). A missing branch is proof rather than a guess: nothing
// downstream wants a plan of a commit nobody can reach.
func TestDeadBecause_OnlyAMissingBranchIsProof(t *testing.T) {
	if why, dead := deadBecause(false); !dead || why == "" {
		t.Errorf("a run whose branch is gone should be cleared, and should say why; got %q, %v", why, dead)
	}
	if why, dead := deadBecause(true); dead {
		t.Errorf("a run on a branch that still exists was judged dead (%q)", why)
	}
}

// The statuses are most of what can go wrong here, and none of it could be
// exercised while the API address was a constant. A 404 is a branch that is
// gone; a 409 is a run that finished while we were deciding. Either read the
// wrong way cancels work somebody wanted, or leaves a dead run in the queue.

func TestBranchExists_ReadsTheStatusRatherThanTheBody(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   bool
		fails  bool
	}{
		{"the branch is there", http.StatusOK, true, false},
		{"the branch is gone", http.StatusNotFound, false, false},
		{"GitHub is unwell", http.StatusInternalServerError, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			got, err := (&github{repo: "owner/repo", token: "t", api: srv.URL}).branchExists("some-branch")
			if tc.fails {
				if err == nil {
					t.Fatal("an answer that was neither 200 nor 404 should be an error, not a verdict")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("branchExists = %v, want %v", got, tc.want)
			}
		})
	}
}

// A run that finished while we were deciding answers 409, which is not a
// failure worth reporting: it is no longer queued either way.
func TestCancel_TreatsAnAlreadyFinishedRunAsDone(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		fails  bool
	}{
		{"accepted", http.StatusAccepted, false},
		{"already finished", http.StatusConflict, false},
		{"refused", http.StatusForbidden, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("cancel used %s, not POST", r.Method)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"message":"no"}`))
			}))
			defer srv.Close()

			err := (&github{repo: "owner/repo", token: "t", api: srv.URL}).cancel(42)
			if tc.fails != (err != nil) {
				t.Errorf("cancel error = %v, want failure: %v", err, tc.fails)
			}
		})
	}
}

// A person is not refuse. A run held at an environment approval is waiting for
// somebody, which is the system working, and the sweeper never touches it.
func TestWaitingOnAPerson_SeparatesApprovalsFromTheRest(t *testing.T) {
	approvals := map[int64]bool{1: true, 3: true}
	held, rest := waitingOnAPerson([]workflowRun{{ID: 1}, {ID: 2}, {ID: 3}},
		func(id int64) (bool, error) { return approvals[id], nil })

	if len(held) != 2 || len(rest) != 1 || rest[0].ID != 2 {
		t.Errorf("held = %v, rest = %v; want 1 and 3 held", held, rest)
	}
}

// "I could not ask" is not "nobody is waiting" - but for a program that
// cancels things, it also is not permission. A run whose state could not be
// established is left in the list and then judged on its branch, which is
// evidence this program can see for itself.
func TestWaitingOnAPerson_KeepsRunsItCouldNotAskAbout(t *testing.T) {
	held, rest := waitingOnAPerson([]workflowRun{{ID: 7}},
		func(int64) (bool, error) { return false, http.ErrServerClosed })

	if len(held) != 0 || len(rest) != 1 {
		t.Errorf("held = %v, rest = %v; want the run kept for judging", held, rest)
	}
}

// The verb end to end, against a stub: what it counts as a candidate, what it
// treats as proof, and the difference between reporting and acting.
func TestClearWith_CancelsOnlyDeadRunsAndOnlyWhenConfirmed(t *testing.T) {
	var cancelled []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/actions/runs") && r.URL.Query().Get("status") == "queued":
			_, _ = w.Write([]byte(`{"workflow_runs":[
				{"id":1,"status":"queued","head_branch":"gone"},
				{"id":2,"status":"queued","head_branch":"alive"},
				{"id":3,"status":"queued","head_branch":"held"}]}`))
		case strings.HasSuffix(r.URL.Path, "/actions/runs"):
			_, _ = w.Write([]byte(`{"workflow_runs":[]}`))
		case strings.HasSuffix(r.URL.Path, "/pending_deployments"):
			if strings.Contains(r.URL.Path, "/runs/3/") {
				_, _ = w.Write([]byte(`[{"environment":{"name":"management"}}]`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "/branches/gone"):
			w.WriteHeader(http.StatusNotFound)
		case strings.Contains(r.URL.Path, "/branches/"):
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/cancel"):
			cancelled = append(cancelled, r.URL.Path)
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := &github{repo: "owner/repo", token: "t", api: srv.URL}

	if code := clearWith(c, false); code != 0 {
		t.Fatalf("a dry run should succeed, got exit %d", code)
	}
	if len(cancelled) != 0 {
		t.Fatalf("a dry run cancelled something: %v", cancelled)
	}

	if code := clearWith(c, true); code != 0 {
		t.Fatalf("clearing should succeed, got exit %d", code)
	}
	if len(cancelled) != 1 || !strings.Contains(cancelled[0], "/runs/1/") {
		t.Errorf("cancelled = %v; only run 1 is dead - 2 is on a live branch and 3 is waiting "+
			"for a person, which is the system working rather than refuse", cancelled)
	}
}
