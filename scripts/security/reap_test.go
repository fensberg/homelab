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
		t.Errorf("a run whose branch is gone should be reaped, and should say why; got %q, %v", why, dead)
	}
	if why, dead := deadBecause(true); dead {
		t.Errorf("a run on a branch that still exists was judged dead (%q)", why)
	}
}

// The HTTP is most of what can go wrong with these checks, and none of it was
// exercised while the address was a constant. A 404 is a branch that is gone,
// a 409 is a run that finished while we were deciding, and either read the
// wrong way either cancels work somebody wanted or leaves a dead run queued.

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

			c := &client{repo: "owner/repo", token: "t", api: srv.URL}
			got, err := c.branchExists("some-branch")
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

// A run that finished while we were deciding answers 409, and that is not a
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

			err := (&client{repo: "owner/repo", token: "t", api: srv.URL}).cancel(42)
			if tc.fails != (err != nil) {
				t.Errorf("cancel error = %v, want failure: %v", err, tc.fails)
			}
		})
	}
}

// whyDead asks about the branch and says why, so the reason reaches the job
// summary rather than a bare list of ids.
func TestWhyDead_SaysWhyAndOnlyForAMissingBranch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/branches/gone") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := &client{repo: "owner/repo", token: "t", api: srv.URL}

	why, dead, err := c.whyDead(run{ID: 1, Branch: "gone"})
	if err != nil || !dead || why == "" {
		t.Errorf("a run on a deleted branch: why=%q dead=%v err=%v", why, dead, err)
	}

	if _, dead, err := c.whyDead(run{ID: 2, Branch: "still-here"}); err != nil || dead {
		t.Errorf("a run on a live branch was judged dead (err=%v)", err)
	}

	// No branch at all - a workflow_dispatch of a workflow file, say - is not
	// evidence of anything.
	if _, dead, err := c.whyDead(run{ID: 3}); err != nil || dead {
		t.Errorf("a run with no branch was judged dead (err=%v)", err)
	}
}

// The patrol's own question, answered against a stub: empty means nobody is
// waiting, a populated list means it is somebody's turn.
func TestAwaitingApproval_EmptyMeansNobodyIsWaiting(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"nobody", `[]`, false},
		{"an environment is waiting", `[{"environment":{"name":"management"}}]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			got, err := (&client{repo: "owner/repo", token: "t", api: srv.URL}).awaitingApproval(7)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("awaitingApproval = %v, want %v", got, tc.want)
			}
		})
	}
}

// runs() asks the right place and parses what comes back.
func TestRuns_ParsesThePage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("status"); got != "queued" {
			t.Errorf("asked for status=%q, want queued", got)
		}
		_, _ = w.Write([]byte(`{"workflow_runs":[{"id":11,"status":"queued","head_branch":"gone"}]}`))
	}))
	defer srv.Close()

	got, err := (&client{repo: "owner/repo", token: "t", api: srv.URL}).runs("", "status=queued")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != 11 || got[0].Branch != "gone" {
		t.Errorf("runs = %+v, want the one queued run", got)
	}
}

// The verb end to end, against a stub: what it counts as a candidate, what it
// treats as proof, and the difference between reporting and acting.
func TestReapWith_CancelsOnlyDeadRunsAndOnlyWhenConfirmed(t *testing.T) {
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
			// Run 3 is waiting for a person, and is never anybody's to cancel.
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
	c := &client{repo: "owner/repo", token: "t", api: srv.URL}

	if code := reapWith(c, false); code != 0 {
		t.Fatalf("a dry run should succeed, got exit %d", code)
	}
	if len(cancelled) != 0 {
		t.Fatalf("a dry run cancelled something: %v", cancelled)
	}

	if code := reapWith(c, true); code != 0 {
		t.Fatalf("reaping should succeed, got exit %d", code)
	}
	if len(cancelled) != 1 || !strings.Contains(cancelled[0], "/runs/1/") {
		t.Errorf("cancelled = %v; only run 1 is dead - 2 is on a live branch and 3 is waiting "+
			"for a person, which is the system working rather than a fault", cancelled)
	}
}
