package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The retry helper, run for real.
//
// WHY IT EXISTS. The Validate lane failed on a 500 from GitHub's release CDN
// while fetching a pinned provider's checksums - a failure nothing in the pull
// request could cause and nothing in it could fix.
//
// WHY IT IS TESTED RATHER THAN TRUSTED. A retry is the one kind of helper whose
// bugs are invisible: one that never retries looks exactly like a green run,
// and one that retries too eagerly turns a real failure into a flake nobody
// investigates. Both failure modes are silent, and both are worse than not
// having it.
//
// covers: shell:scripts/retry.sh

// retryFixture runs the shipped script against a command whose failures are
// scripted, and counts how many times it was actually called.
type retryFixture struct {
	dir, counter string
	env          []string
}

// newRetryFixture builds a command that fails the first failFirst times and
// succeeds after, recording every invocation.
func newRetryFixture(t *testing.T, failFirst int) *retryFixture {
	t.Helper()
	dir := t.TempDir()
	counter := filepath.Join(dir, "calls")
	// Named for a command the script permits. There is deliberately no
	// test-only bypass of the refusal - an escape hatch a test can use is one
	// a caller can use - so the fixture has to be something genuinely
	// retryable, and it is invoked by a path containing that name.
	script := filepath.Join(dir, "curl")

	body := "#!/usr/bin/env bash\n" +
		"n=$(( $(cat \"$COUNTER\" 2>/dev/null || echo 0) + 1 ))\n" +
		"echo \"$n\" > \"$COUNTER\"\n" +
		"[ \"$n\" -le " + strconv.Itoa(failFirst) + " ] && exit 7\n" +
		"exit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("writing the flaky command: %v", err)
	}
	return &retryFixture{
		dir:     dir,
		counter: counter,
		env:     append(os.Environ(), "COUNTER="+counter),
	}
}

func (f *retryFixture) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := repoRoot(t)
	cmd := exec.Command("bash", append([]string{root + "/scripts/retry.sh"}, args...)...)
	cmd.Env = f.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (f *retryFixture) calls(t *testing.T) int {
	t.Helper()
	body, err := os.ReadFile(f.counter)
	if err != nil {
		return 0
	}
	n, convErr := strconv.Atoi(strings.TrimSpace(string(body)))
	if convErr != nil {
		t.Fatalf("reading the call count: %v", convErr)
	}
	return n
}

// A command that fails once and then succeeds is retried, and the run is green.
func TestRetryOutlastsATransientFailure(t *testing.T) {
	f := newRetryFixture(t, 1)

	out, err := f.run(t, "3", "0", filepath.Join(f.dir, "curl"))
	if err != nil {
		t.Fatalf("a command that succeeds on its second attempt was reported as failed: %v\n%s", err, out)
	}
	if calls := f.calls(t); calls != 2 {
		t.Errorf("the command ran %d time(s), want 2 - once failing, once succeeding", calls)
	}
}

// A retry that succeeded says so.
//
// This is the assertion that keeps a degrading dependency visible. A fetch
// needing three attempts on every single run is a finding, and a silent retry
// turns it into the new normal - which is the shape this repository refuses
// everywhere else: "it worked" and "it worked eventually, after failing twice"
// are different facts.
func TestASuccessfulRetryIsNotSilent(t *testing.T) {
	f := newRetryFixture(t, 2)

	out, err := f.run(t, "3", "0", filepath.Join(f.dir, "curl"))
	if err != nil {
		t.Fatalf("retry: %v\n%s", err, out)
	}
	if !strings.Contains(out, "succeeded on attempt 3") {
		t.Errorf(`a run that failed twice before succeeding said nothing about it:

%s
A dependency that always needs three attempts is degrading, and a green run
that hides that is how it becomes the new normal.`, out)
	}
}

// A command that always fails still fails, after a bounded number of tries.
//
// The whole point of a bound. An unbounded retry against a metered vendor is a
// bill nobody sees coming, and a retry that eventually reports success would be
// worse than no retry at all.
func TestRetryGivesUpAndFails(t *testing.T) {
	f := newRetryFixture(t, 99)

	out, err := f.run(t, "3", "0", filepath.Join(f.dir, "curl"))
	if err == nil {
		t.Fatalf("a command that never succeeds was reported as successful:\n%s", out)
	}
	if calls := f.calls(t); calls != 3 {
		t.Errorf("the command ran %d time(s), want exactly the 3 attempts asked for", calls)
	}
	if !strings.Contains(out, "is not being retried again") {
		t.Errorf("giving up was not stated:\n%s", out)
	}
}

// One attempt means one attempt.
//
// The boundary case, because an off-by-one here silently doubles every caller's
// work against a vendor.
func TestOneAttemptRunsOnce(t *testing.T) {
	f := newRetryFixture(t, 99)

	if _, err := f.run(t, "1", "0", filepath.Join(f.dir, "curl")); err == nil {
		t.Fatal("a failing command reported success")
	}
	if calls := f.calls(t); calls != 1 {
		t.Errorf("the command ran %d time(s) for 1 attempt", calls)
	}
}

// Nonsense arguments are refused rather than interpreted.
//
// A retry told to run "lots" of times, or handed a delay it reads as zero, is a
// retry doing something other than what the caller meant - and the caller would
// never find out, because the command usually succeeds.
func TestRetryRefusesArgumentsItCannotHonour(t *testing.T) {
	f := newRetryFixture(t, 0)
	flaky := filepath.Join(f.dir, "curl")

	cases := []struct{ name, attempts, delay string }{
		{"attempts is not a number", "lots", "0"},
		{"delay is not a number", "3", "soon"},
		{"attempts is zero", "0", "0"},
		{"attempts above the ceiling", "20", "0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := f.run(t, tc.attempts, tc.delay, flaky)
			if err == nil {
				t.Fatalf("accepted %q attempts and %q delay:\n%s", tc.attempts, tc.delay, out)
			}
			if f.calls(t) != 0 {
				t.Error("the command was run despite the arguments being refused")
			}
		})
	}
}

// The helper refuses anything that is not a declared fetch.
//
// THE LINE THAT MATTERS, and it is enforced by the script rather than by this
// test. Retrying a check until it passes is how a real failure becomes a flake
// nobody investigates, and it would be indistinguishable from the check
// working - the run goes green either way.
//
// The refusal lives in scripts/retry.sh so it applies to every caller that
// exists now and every one written later, whether or not anybody remembered to
// extend a guard. This asserts the mechanism works; it is not the mechanism.
func TestRetryRefusesAnythingThatIsNotAFetch(t *testing.T) {
	f := newRetryFixture(t, 0)

	// Verdicts, and a plain unknown command. None may be retried.
	for _, command := range [][]string{
		{"tofu", "validate"},
		{"tofu", "test"},
		{"go", "test", "./..."},
		{"task", "validate"},
		{"pre-commit", "run", "--all-files"},
		{"kubeconform"},
		{"some-tool-nobody-has-declared"},
	} {
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			out, err := f.run(t, append([]string{"3", "0"}, command...)...)
			if err == nil {
				t.Fatalf(`retrying %q was permitted.

Retrying something that answers a question about the change turns a real
failure into a flake nobody investigates.`, strings.Join(command, " "))
			}
			if !strings.Contains(out, "refusing to retry") {
				t.Errorf("refused without saying why:\n%s", out)
			}
			if !strings.Contains(out, "RETRYABLE in scripts/retry.sh") {
				t.Errorf(`the refusal does not say what to do if this really is a fetch:

%s
A refusal naming no remedy is one somebody works around.`, out)
			}
		})
	}
}

// The default is refusal, so a command nobody thought about is refused.
//
// This is the property that makes the list cover FUTURE work rather than only
// what existed when it was written. A permit-list that defaulted to allowing
// would silently cover every new caller, which is the allow-list failure this
// repository refuses everywhere else.
func TestAnUndeclaredCommandIsRefusedRatherThanAllowed(t *testing.T) {
	f := newRetryFixture(t, 0)

	declared := readRepoFile(t, "scripts/retry.sh")
	if !strings.Contains(declared, "RETRYABLE=(") {
		t.Fatal("scripts/retry.sh no longer declares a RETRYABLE list, so this test " +
			"is checking a mechanism that is gone")
	}

	out, err := f.run(t, "2", "0", "a-command-invented-after-this-was-written")
	if err == nil {
		t.Fatalf(`a command nobody declared was retried:

%s
The default has to be refusal. A retry helper that permits anything it was not
told about covers every future caller silently, which is exactly the failure a
block list exists to prevent.`, out)
	}
}
