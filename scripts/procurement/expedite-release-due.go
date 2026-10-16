package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	// The zone database, compiled in.
	//
	// A runner image without tzdata answers a zone's name with an error,
	// and the honest failure of a release gate is to refuse rather than to
	// assume UTC - which would put the release six hours out and land it in the
	// middle of the evening. Embedding it removes the question.
	_ "time/tzdata"
)

// Due reports whether now is the hour when a delivery may be taken.
//
// The estate releases at 4am on the first site's clock (its timezone in
// config/management.tpl.json, the first site by name), because that is when
// a restart disturbs nobody: production reconciles from the tag, the deployment's strategy is
// Recreate, and everyone connected is dropped when it lands.
//
// Local, not UTC, and that is the whole reason this is a function rather than a
// cron expression. GitHub's scheduler speaks only UTC, so a fixed cron drifts
// an hour twice a year: 09:00 UTC is 4am in summer and 3am in winter. The
// workflow therefore asks every hour and lets this say whether it is the one.
func Due(now time.Time, zone string, hour int) (bool, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return false, fmt.Errorf("no such time zone %q: %w", zone, err)
	}
	return now.In(loc).Hour() == hour, nil
}

func expediteReleaseDue(args []string) int {
	fs := flag.NewFlagSet("expedite-release-due", flag.ContinueOnError)
	// No default: the zone is the first site's, declared in the config
	// template, and one written here would be a second place to change.
	zone := fs.String("zone", "", "the zone whose clock the release keeps: the first site's timezone")
	hour := fs.Int("hour", 4, "the hour a delivery may be taken, in that zone")
	at := fs.String("at", "", "the instant to judge, RFC 3339 (default: now)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *zone == "" {
		fmt.Fprintln(os.Stderr, "procurement expedite-release-due: -zone is required: the first site's timezone, from config/management.tpl.json")
		return 2
	}

	now := time.Now().UTC()
	if *at != "" {
		parsed, err := time.Parse(time.RFC3339, *at)
		if err != nil {
			fmt.Fprintf(os.Stderr, "procurement expedite-release-due: -at is not RFC 3339: %v\n", err)
			return 2
		}
		now = parsed
	}

	due, err := Due(now, *zone, *hour)
	if err != nil {
		fmt.Fprintf(os.Stderr, "procurement expedite-release-due: %v\n", err)
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
