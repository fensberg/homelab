package fit

import (
	"strings"
	"testing"
)

const gib = int64(1) << 30

// A site of three workers that hold nine gibibytes each, with eleven
// reserved by work that cannot wait.
func standing() Site {
	return Site{Workers: 27 * gib, LargestWorker: 9 * gib, MustRun: 11 * gib}
}

// A change that asks for more than is left with a worker gone is refused,
// and the refusal carries the whole sum so that it can be argued with.
func TestAChangeThatLeavesNoRoomWithAWorkerGoneIsRefused(t *testing.T) {
	line := Of(standing(), Change{Added: 8 * gib, LargestPod: 4 * gib})
	if !line.Refused() {
		t.Fatalf("eleven reserved and eight more, in eighteen with a worker gone, was let through: %s", line)
	}
	err := Refusal(line)
	if err == nil {
		t.Fatal("a refused change produced no refusal")
	}
	for _, said := range []string{"27.0", "18.0", "11.0", "8.0", "OVER BY 1.0"} {
		if !strings.Contains(err.Error(), said) {
			t.Errorf("the refusal does not say %q, so whoever reads it cannot see which figure to question:\n%s", said, err)
		}
	}
}

// What fits is let through, to the gibibyte.
func TestAChangeThatFitsWithAWorkerGoneIsLetThrough(t *testing.T) {
	line := Of(standing(), Change{Added: 7 * gib, LargestPod: 4 * gib})
	if line.Refused() || Refusal(line) != nil {
		t.Fatalf("eleven and seven in eighteen was refused: %s", line)
	}
	if !strings.Contains(line.String(), "fits, with 0.0 to spare") {
		t.Errorf("a change that fits exactly does not say so: %s", line)
	}
}

// Tight does not mean seized. A site already over is not made safer by
// refusing a change that asks for no more: the change that would shrink it
// is among the ones a flat refusal blocks.
func TestASiteAlreadyOverIsNotStoppedByAChangeThatAsksNoMore(t *testing.T) {
	over := Site{Workers: 27 * gib, LargestWorker: 9 * gib, MustRun: 20 * gib}
	for name, change := range map[string]Change{
		"asks nothing":    {},
		"gives some back": {Added: 1 * gib, Removed: 2 * gib},
	} {
		line := Of(over, change)
		if line.Refused() {
			t.Errorf("a site already over was stopped by a change that %s: %s", name, line)
		}
		if !strings.Contains(line.String(), "OVER BY") {
			t.Errorf("a site that is over was not told so by a change that %s: %s", name, line)
		}
	}
	if line := Of(over, Change{Added: 1 * gib}); !line.Refused() {
		t.Errorf("a site already over was given more: %s", line)
	}
}

// A single pod larger than any one worker can hold is refused whatever the
// total: room that is spread across three machines is not room for it.
func TestAPodThatFitsOnNoWorkerIsRefused(t *testing.T) {
	line := Of(standing(), Change{Added: 10*gib - 4*gib, LargestPod: 10 * gib})
	if !line.Refused() {
		t.Fatalf("a ten gibibyte pod was let onto workers that hold nine: %s", line)
	}
	if err := Refusal(line); err == nil || !strings.Contains(err.Error(), "no one worker") {
		t.Errorf("the refusal does not say the pod fits on no one worker: %v", err)
	}
}

// A site nobody has read cannot be held against anything. That is said as
// what it is and never as a change that fits.
func TestASiteNobodyHasReadIsAnErrorAndNotAFit(t *testing.T) {
	line := Of(Site{}, Change{Added: 1 * gib, LargestPod: 1 * gib})
	if err := Refusal(line); err == nil || !strings.Contains(err.Error(), "has not been read") {
		t.Errorf("a change was held against a site with no workers read, and the answer was: %v", err)
	}
	// A change that asks for nothing is not stopped for it: a record with no
	// standing must not hold up every change that touches no workload.
	if err := Refusal(Of(Site{}, Change{})); err != nil {
		t.Errorf("a change that asks for nothing was stopped because the site was not read: %v", err)
	}
	if err := Refusal(Of(Site{}, Change{Removed: 1 * gib})); err != nil {
		t.Errorf("a change that only gives back was stopped because the site was not read: %v", err)
	}
}
