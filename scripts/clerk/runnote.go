package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

// The run speaks once, and only about what the whole run found.
//
// Both verbs run on a pull request and only one of them was ever given -pr,
// deliberately: two comments repeating one count is noise. The consequence went
// unlooked-for until #335. handover filed fourteen alerts, snag found nothing,
// and the only voice in the run said "nothing to raise" on a pull request
// carrying fifteen open findings. The comment was true about snagging and read
// as a verdict on the run.
//
// So no verb comments any more. Each writes its report, and this reads all of
// them and composes one note from the lot. What that note says is unchanged -
// silence when the findings are already on the diff, the unseen ones named when
// they are not, and a clean reading declared only when the run really was
// clean. The only thing that moved is who is entitled to say it.

// gather reads every report a run produced into one view of it.
//
// A report that cannot be read is an error rather than an empty one. The
// estate's rule is that a check unable to examine its subject says so instead
// of returning a third, calmer status - and treating an unreadable report as
// "no findings" would turn a failure to look into a clean verdict, which is the
// one thing this surface must never do.
func gather(paths []string) ([]snag, int, error) {
	var (
		found     []snag
		discarded int
	)
	for _, p := range paths {
		body, err := os.ReadFile(p)
		if err != nil {
			return nil, 0, fmt.Errorf("reading %s: %w", p, err)
		}
		var r struct {
			Runs []struct {
				Properties struct {
					Discarded int `json:"discarded"`
				} `json:"properties"`
				Results []struct {
					RuleID  string `json:"ruleId"`
					Message struct {
						Text string `json:"text"`
					} `json:"message"`
					Locations []struct {
						PhysicalLocation struct {
							ArtifactLocation struct {
								URI string `json:"uri"`
							} `json:"artifactLocation"`
							Region struct {
								StartLine int `json:"startLine"`
							} `json:"region"`
						} `json:"physicalLocation"`
					} `json:"locations"`
				} `json:"results"`
			} `json:"runs"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, 0, fmt.Errorf("parsing %s: %w", p, err)
		}
		for _, run := range r.Runs {
			discarded += run.Properties.Discarded
			for _, res := range run.Results {
				s := snag{Rule: res.RuleID, Message: res.Message.Text}
				if len(res.Locations) > 0 {
					loc := res.Locations[0].PhysicalLocation
					s.Path = loc.ArtifactLocation.URI
					s.Line = loc.Region.StartLine
				}
				found = append(found, s)
			}
		}
	}
	return found, discarded, nil
}

// noteVerb posts the one note a run is entitled to.
func noteVerb(args []string) int {
	fs := flag.NewFlagSet("clerk note", flag.ContinueOnError)
	pr := fs.Int("pr", 0, "post the run's note on this pull request")
	name := fs.String("name", "reading", "what to call this run in the note")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	reports := fs.Args()
	if len(reports) == 0 {
		fmt.Fprintln(os.Stderr, "clerk note: name at least one report to read")
		return 2
	}
	if *pr == 0 {
		fmt.Fprintln(os.Stderr, "clerk note: -pr is required; there is nowhere else to post")
		return 2
	}

	kept, discarded, err := gather(reports)
	if err != nil {
		fmt.Fprintln(os.Stderr, "clerk:", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "clerk note: %d finding(s) across %d report(s), %d discarded as uncheckable\n",
		len(kept), len(reports), discarded)

	// Which of them the pull request will already display. Only the ones it
	// will not are worth a comment; see note.
	var (
		unseen []snag
		caveat string
	)
	if len(kept) > 0 {
		shown, err := shownLines(*pr)
		switch {
		case err != nil:
			// Cannot tell, so assume the worst rather than the convenient
			// thing: a comment too many costs a line, and being wrong the other
			// way means findings nobody ever sees.
			fmt.Fprintln(os.Stderr, "clerk:", err)
			unseen = kept
			if caveat == "" {
				caveat = "could not read which lines this pull request shows, so every finding is listed"
			}
		default:
			for _, s := range kept {
				if !shown[s.Path][s.Line] {
					unseen = append(unseen, s)
				}
			}
		}
		fmt.Fprintf(os.Stderr, "clerk note: %d of %d finding(s) fall outside the diff\n", len(unseen), len(kept))
	}

	// discarded is passed as a count rather than as reasons: the reasons were
	// printed by the verb that discarded them, and this surface only ever
	// carried the number.
	reasons := make([]string, discarded)
	if body := note(*name, kept, unseen, reasons, caveat); body != "" {
		if code := post(*pr, body); code != 0 {
			return code
		}
	}
	return 0
}
