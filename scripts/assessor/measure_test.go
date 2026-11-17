package main

import (
	"reflect"
	"testing"
	"time"
)

// The first push is the earliest pull-request run on the branch while the
// pull request was open - not a push run, not another branch, not a run
// from before it was opened or after it closed.
func TestTheFirstPushIsTheEarliestRunWhileThePullRequestWasOpen(t *testing.T) {
	p := pull{CreatedAt: at("2026-01-05T00:00:00Z"), ClosedAt: at("2026-01-06T00:00:00Z")}
	p.Head.Ref = "piece"
	runs := []workflowRun{
		{Event: "push", HeadBranch: "piece", HeadSHA: "pushed", CreatedAt: at("2026-01-05T00:00:30Z")},
		{Event: "pull_request", HeadBranch: "other", HeadSHA: "other", CreatedAt: at("2026-01-05T00:00:30Z")},
		{Event: "pull_request", HeadBranch: "piece", HeadSHA: "before", CreatedAt: at("2026-01-04T00:00:00Z")},
		{Event: "pull_request", HeadBranch: "piece", HeadSHA: "after", CreatedAt: at("2026-01-07T00:00:00Z")},
		{Event: "pull_request", HeadBranch: "piece", HeadSHA: "second", CreatedAt: at("2026-01-05T02:00:00Z")},
		{Event: "pull_request", HeadBranch: "piece", HeadSHA: "first", CreatedAt: at("2026-01-05T00:01:00Z")},
	}
	if sha, _, ok := firstPush(p, runs); !ok || sha != "first" {
		t.Errorf("got %q, %v; want the first run while open", sha, ok)
	}
	if _, _, ok := firstPush(p, runs[:4]); ok {
		t.Error("a pull request with no run of its own was given one")
	}
	open := p
	open.ClosedAt = time.Time{}
	if sha, _, _ := firstPush(open, runs[3:4]); sha != "after" {
		t.Errorf("a pull request still open takes runs up to now; got %q", sha)
	}
}

// Each way a first push can come out.
func TestAFirstPushIsJudgedOnEachChecksFirstAttempt(t *testing.T) {
	// A check run's number is when it started, in the order GitHub gave out.
	c := func(name, conclusion, started string) check {
		return check{at(started).Unix(), name, conclusion}
	}
	for name, tc := range map[string]struct {
		checks   []check
		required []string
		leaveOut string
		want     verdict
	}{
		"every check passed":          {[]check{c("a", "success", "2026-01-01T00:00:00Z"), c("b", "skipped", "2026-01-01T00:00:00Z")}, nil, "", passed},
		"one failed":                  {[]check{c("a", "success", "2026-01-01T00:00:00Z"), c("b", "failure", "2026-01-01T00:00:00Z")}, nil, "", failed},
		"a re-run passed later":       {[]check{c("a", "success", "2026-01-01T01:00:00Z"), c("a", "failure", "2026-01-01T00:00:00Z")}, nil, "", failed},
		"cancelled by the next push":  {[]check{c("a", "success", "2026-01-01T00:00:00Z"), c("b", "cancelled", "2026-01-01T00:00:00Z")}, nil, "", unmeasured},
		"a failure outranks a cancel": {[]check{c("a", "cancelled", "2026-01-01T00:00:00Z"), c("b", "timed_out", "2026-01-01T00:00:00Z")}, nil, "", failed},
		"no checks at all":            {nil, nil, "", unmeasured},
		"the gate is left out":        {[]check{c("a", "success", "2026-01-01T00:00:00Z"), c("gate", "failure", "2026-01-01T00:00:00Z")}, nil, "gate", passed},
		"only the gate ran":           {[]check{c("gate", "failure", "2026-01-01T00:00:00Z")}, nil, "gate", unmeasured},
		"a check not required failed": {[]check{c("a", "success", "2026-01-01T00:00:00Z"), c("b", "failure", "2026-01-01T00:00:00Z")}, []string{"a"}, "", passed},
		"a required check never ran":  {[]check{c("a", "success", "2026-01-01T00:00:00Z")}, []string{"a", "b"}, "", unmeasured},
		"the required gate left out":  {[]check{c("a", "success", "2026-01-01T00:00:00Z"), c("gate", "failure", "2026-01-01T00:00:00Z")}, []string{"a", "gate"}, "gate", passed},
	} {
		if got := judge(tc.checks, tc.required, tc.leaveOut); got != tc.want {
			t.Errorf("%s: got %d, want %d", name, got, tc.want)
		}
	}
}

// The required set is the one declared most recently at the moment, and
// nothing before the first.
func TestTheRequiredSetIsTheOneInForceThatDay(t *testing.T) {
	history := []requirement{
		{at("2026-01-10T00:00:00Z"), []string{"a"}},
		{at("2026-01-20T00:00:00Z"), []string{"b"}},
	}
	for when, want := range map[string][]string{
		"2026-01-01T00:00:00Z": nil,
		"2026-01-10T00:00:00Z": {"a"},
		"2026-01-19T00:00:00Z": {"a"},
		"2026-02-01T00:00:00Z": {"b"},
	} {
		if got := requiredOn(history, at(when)); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v, want %v", when, got, want)
		}
	}
}

// A rate is of the measured only, and there is none without any.
func TestARateCountsOnlyWhatWasMeasured(t *testing.T) {
	var tl tally
	if _, ok := tl.rate(); ok {
		t.Error("an empty tally gave a rate")
	}
	for _, v := range []verdict{passed, passed, failed, unmeasured} {
		tl.add(v)
	}
	if r, ok := tl.rate(); !ok || r < 66.6 || r > 66.7 || tl.Unmeasured != 1 {
		t.Errorf("got %v %v %+v; want two of three", r, ok, tl)
	}
}

// A failed job is put in its family by name; the gate that only repeats
// the other lanes is in none, and a name no family claims is Other.
func TestAFailedJobIsPutInItsFamily(t *testing.T) {
	j := func(name string, steps ...string) job {
		out := job{Name: name}
		for _, s := range steps {
			out.Steps = append(out.Steps, struct {
				Name       string `json:"name"`
				Conclusion string `json:"conclusion"`
			}{s, "failure"})
		}
		return out
	}
	for name, want := range map[string]string{
		"test / repo policy":                  "Repository guards (Go, Conftest and Semgrep rules of the estate)",
		"Repository invariants":               "Repository guards (Go, Conftest and Semgrep rules of the estate)",
		"test / mutation (2 of 4)":            "Mutation proofs of the guards",
		"the SENSITIVE PATHS tripwire":        "Acknowledgement gate (waits for a person)",
		"Spelling (codespell)":                "Spelling",
		"tests passed":                        "",
		"something nobody has named a family": "Other",
	} {
		if got := familyOf(j(name)); got != want {
			t.Errorf("%q: got %q, want %q", name, got, want)
		}
	}
	if got := familyOf(j("an old lane", "Every lane passed")); got != "" {
		t.Errorf("an old gate step was given the family %q", got)
	}
}

// A log is read for each kind of thing that failed in it, once each, with
// its timestamps and colours taken off first.
func TestALogIsReadForWhatFailedInIt(t *testing.T) {
	log := "2026-01-01T00:00:00.1234567Z \x1b[31m--- FAIL: TestOne (0.01s)\x1b[0m\n" +
		"2026-01-01T00:00:00.1234567Z --- FAIL: TestOne (0.01s)\n" +
		"    FAIL - policy/x.yml - main - kept_rule: what it says\n" +
		"  ❯❯❱ go.lang.security.something\n" +
		"prettier.................................................................Failed\n"
	want := []finding{
		{"Conftest rule", "kept_rule"},
		{"Go test", "TestOne"},
		{"Semgrep registry rule", "go.lang.security.something"},
		{"pre-commit hook", "prettier"},
	}
	if got := findings(log); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
	if got := findings(""); len(got) != 0 {
		t.Errorf("an empty log named %v", got)
	}
}

// A guard is paired with the newest issue it cites that is older than it;
// one citing only younger issues, or issues not known, is not paired.
func TestAGuardIsPairedWithTheIssueThatCameBeforeIt(t *testing.T) {
	added := at("2026-02-01T00:00:00Z")
	g := guard{Kind: "Go test", Name: "TestX", File: "guards/x_test.go", Issues: []int{1, 2, 3, 4}, Added: added}
	opened := map[int]time.Time{
		1: at("2026-01-01T00:00:00Z"),
		2: at("2026-01-20T00:00:00Z"),
		3: at("2026-03-01T00:00:00Z"), // after the guard: a follow-up, not its cause
	}
	f := func(name, when, openedAt string, touched ...string) failure {
		return failure{finding{"Go test", name}, at(when), at(openedAt), touched, 0}
	}
	failures := []failure{
		f("TestX", "2026-01-25T00:00:00Z", "2026-01-25T00:00:00Z"),                // before the guard
		f("TestX", "2026-02-02T00:00:00Z", "2026-01-30T00:00:00Z"),                // its pull request was open already
		f("TestX", "2026-02-10T00:00:00Z", "2026-02-10T00:00:00Z", "README.md"),   // a plain repeat
		f("TestX", "2026-02-11T00:00:00Z", "2026-02-11T00:00:00Z", "guards/y.go"), // the checks were being changed
		f("TestY", "2026-02-12T00:00:00Z", "2026-02-12T00:00:00Z"),                // another guard
	}
	got := incidents([]guard{g, {Name: "TestUnpaired", Issues: []int{3, 9}, Added: added}}, opened, failures, []string{"guards"})
	if len(got) != 1 {
		t.Fatalf("got %d incidents, want one: %+v", len(got), got)
	}
	inc := got[0]
	if inc.Issue != 2 || inc.Lead != 12*24*time.Hour || inc.Repeats != 2 || inc.Plain != 1 ||
		!inc.LastSeen.Equal(at("2026-02-11T00:00:00Z")) {
		t.Errorf("got %+v", inc)
	}
}

// The rates either side of a moment are of the last and first window
// measured pull requests, and there is no answer without enough.
func TestTheRatesAroundAnEditAreOfTheNearestPullRequests(t *testing.T) {
	m := func(when string, v verdict) measured { return measured{at(when), v} }
	prs := []measured{
		m("2026-01-01T00:00:00Z", passed), // outside the window
		m("2026-01-02T00:00:00Z", failed),
		m("2026-01-03T00:00:00Z", unmeasured),
		m("2026-01-04T00:00:00Z", passed),
		m("2026-01-06T00:00:00Z", passed),
		m("2026-01-07T00:00:00Z", passed),
		m("2026-01-08T00:00:00Z", failed), // outside the window
	}
	before, after, ok := around(prs, at("2026-01-05T00:00:00Z"), 2)
	if !ok || before != 50 || after != 100 {
		t.Errorf("got %v %v %v; want 50 and 100", before, after, ok)
	}
	if _, _, ok := around(prs, at("2026-01-05T00:00:00Z"), 4); ok {
		t.Error("an answer was given with too few on one side")
	}
}

func TestTheMedianIsTheMiddle(t *testing.T) {
	d := func(n ...int) []time.Duration {
		var out []time.Duration
		for _, x := range n {
			out = append(out, time.Duration(x))
		}
		return out
	}
	for _, tc := range []struct {
		in   []time.Duration
		want time.Duration
	}{{nil, 0}, {d(3, 1, 2), 2}, {d(4, 1, 3, 2), 2}} {
		if got := median(tc.in); got != tc.want {
			t.Errorf("median(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
