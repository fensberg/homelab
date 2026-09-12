package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	// The zone database, compiled in.
	//
	// A runner image without tzdata answers "America/Chicago" with an error,
	// and the honest failure of a release gate is to refuse rather than to
	// assume UTC - which would put the release six hours out and land it in the
	// middle of the evening. Embedding it removes the question.
	_ "time/tzdata"
)

// Due reports whether now is the hour when a delivery may be taken.
//
// The estate releases at 4am local, because that is when a restart disturbs
// nobody: production reconciles from the tag, the deployment's strategy is
// Recreate, and everyone connected is dropped when it lands.
//
// Local, not UTC, and that is the whole reason this is a function rather than a
// cron expression. GitHub's scheduler speaks only UTC, so a fixed cron drifts
// an hour twice a year: 09:00 UTC is 4am in summer and 3am in winter. The
// workflow therefore asks at both 09:00 and 10:00 UTC and lets this say which
// of the two is really 4am in Chicago today.
func Due(now time.Time, zone string, hour int) (bool, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return false, fmt.Errorf("no such time zone %q: %w", zone, err)
	}
	return now.In(loc).Hour() == hour, nil
}

func releaseDue(args []string) int {
	fs := flag.NewFlagSet("release-due", flag.ContinueOnError)
	zone := fs.String("zone", "America/Chicago", "the zone whose clock the estate keeps")
	hour := fs.Int("hour", 4, "the hour a delivery may be taken, in that zone")
	at := fs.String("at", "", "the instant to judge, RFC 3339 (default: now)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	now := time.Now().UTC()
	if *at != "" {
		parsed, err := time.Parse(time.RFC3339, *at)
		if err != nil {
			fmt.Fprintf(os.Stderr, "expediter release-due: -at is not RFC 3339: %v\n", err)
			return 2
		}
		now = parsed
	}

	due, err := Due(now, *zone, *hour)
	if err != nil {
		fmt.Fprintf(os.Stderr, "expediter release-due: %v\n", err)
		return 1
	}

	local, _ := time.LoadLocation(*zone)
	if !due {
		fmt.Println("early")
		fmt.Fprintf(os.Stderr, "it is %s in %s, and deliveries are taken at %02d:00\n",
			now.In(local).Format("15:04"), *zone, *hour)
		return 0
	}
	fmt.Println("due")
	fmt.Fprintf(os.Stderr, "it is %s in %s\n", now.In(local).Format("15:04"), *zone)
	return 0
}
