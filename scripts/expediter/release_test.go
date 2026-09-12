package main

import (
	"testing"
	"time"
)

// 4am in Chicago, on both sides of the daylight-saving change.
//
// This is the whole reason the gate is a function instead of a cron
// expression. GitHub's scheduler speaks UTC only, so the workflow asks at 09:00
// and at 10:00 and this decides which one is really 4am today. Get it wrong and
// the release lands at 3am or 5am - or, in the version where the gate assumed
// UTC, at ten in the evening with everybody online.
func TestDueAnswersFourInTheMorningInChicagoThroughTheDstChange(t *testing.T) {
	const zone = "America/Chicago"

	cases := []struct {
		name string
		utc  string
		want bool
	}{
		// Summer, CDT, UTC-5.
		{"09:00 UTC in July is 4am CDT", "2026-07-15T09:00:00Z", true},
		{"10:00 UTC in July is 5am CDT", "2026-07-15T10:00:00Z", false},
		// Winter, CST, UTC-6.
		{"10:00 UTC in January is 4am CST", "2026-01-15T10:00:00Z", true},
		{"09:00 UTC in January is 3am CST", "2026-01-15T09:00:00Z", false},
		// The evening this must never fire in.
		{"23:00 UTC is six in the evening in Chicago", "2026-07-15T23:00:00Z", false},
		// The day the clocks go forward, 2am CST -> 3am CDT.
		{"09:00 UTC on the spring change is 4am CDT", "2026-03-08T09:00:00Z", true},
		{"10:00 UTC on the spring change is 5am CDT", "2026-03-08T10:00:00Z", false},
		// The day they go back. The change happens at 2am local, so by 09:00
		// UTC it is already CST and 3am - not 4am CDT, which is what the first
		// version of this test wrongly expected.
		{"10:00 UTC on the autumn change is 4am CST", "2026-11-01T10:00:00Z", true},
		{"09:00 UTC on the autumn change is already 3am CST", "2026-11-01T09:00:00Z", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			at, err := time.Parse(time.RFC3339, c.utc)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Due(at, zone, 4)
			if err != nil {
				t.Fatalf("judging %s: %v", c.utc, err)
			}
			if got != c.want {
				local := at.In(mustLoad(t, zone))
				t.Errorf("Due(%s) = %v, want %v (that is %s local)",
					c.utc, got, c.want, local.Format("15:04 MST"))
			}
		})
	}
}

// Exactly one of the two candidates is 4am local, on every day of the year.
//
// This is the property the pair of crons exists to satisfy, and it is worth
// asserting rather than spot-checking. If both were ever due, the estate would
// release twice in one morning; if neither were, it would not release at all
// that day and nobody would find out until somebody could not join.
//
// The first version of this test asserted the opposite for the morning the
// clocks go back, reasoning that both 09:00 and 10:00 UTC are 4am that day.
// They are not: the change happens at 2am local, so 09:00 UTC is already 3am
// CST. The code was right and the test was wrong, which is the direction that
// costs nothing to find out.
func TestExactlyOneCandidateIsFourAmEveryDayOfTheYear(t *testing.T) {
	day := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for day.Year() == 2026 {
		due := 0
		for _, hour := range []int{9, 10} {
			at := time.Date(day.Year(), day.Month(), day.Day(), hour, 0, 0, 0, time.UTC)
			ok, err := Due(at, "America/Chicago", 4)
			if err != nil {
				t.Fatal(err)
			}
			if ok {
				due++
			}
		}
		if due != 1 {
			t.Fatalf("%s: %d of the two candidate hours are 4am in Chicago, want exactly one",
				day.Format("2006-01-02"), due)
		}
		day = day.AddDate(0, 0, 1)
	}
}

// A zone that does not exist refuses, and does not fall back to UTC.
//
// The embedded tzdata is what makes this reliable on any runner image; the
// refusal is what makes a typo visible instead of silently releasing six hours
// out.
func TestDueRefusesAZoneItCannotLoad(t *testing.T) {
	if _, err := Due(time.Now(), "America/Chigard", 4); err == nil {
		t.Fatal("an unknown zone was accepted")
	}
}

// The verb itself, including the flag that lets a test name an instant.
func TestReleaseDueVerbJudgesTheInstantItIsGiven(t *testing.T) {
	if rc := releaseDue([]string{"-at", "2026-07-15T09:00:00Z"}); rc != 0 {
		t.Errorf("a due instant exited %d", rc)
	}
	if rc := releaseDue([]string{"-at", "2026-07-15T23:00:00Z"}); rc != 0 {
		t.Errorf("an early instant is not an error, it is an answer: exited %d", rc)
	}
	if rc := releaseDue([]string{"-at", "the small hours"}); rc == 0 {
		t.Error("an unparseable instant was accepted")
	}
	if rc := releaseDue([]string{"-zone", "America/Chigard", "-at", "2026-07-15T09:00:00Z"}); rc == 0 {
		t.Error("an unknown zone was accepted")
	}
}

func mustLoad(t *testing.T, zone string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatalf("loading %s: %v", zone, err)
	}
	return loc
}
