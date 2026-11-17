package main

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// table writes a Markdown table the way the repository's formatter leaves
// one, so the written report passes the format check as it is generated.
func table(head []string, rows [][]string) string {
	width := make([]int, len(head))
	for _, row := range append([][]string{head}, rows...) {
		for i, cell := range row {
			width[i] = max(width[i], utf8.RuneCountInString(cell), 3)
		}
	}
	var b strings.Builder
	line := func(row []string) {
		for i, cell := range row {
			b.WriteString("| " + cell + strings.Repeat(" ", width[i]-utf8.RuneCountInString(cell)) + " ")
		}
		b.WriteString("|\n")
	}
	line(head)
	rule := make([]string, len(head))
	for i := range head {
		rule[i] = strings.Repeat("-", width[i])
	}
	line(rule)
	for _, row := range rows {
		line(row)
	}
	return b.String()
}

// share writes a tally as "passed of measured (rate)".
func share(t tally) string {
	rate, ok := t.rate()
	if !ok {
		return "none measured"
	}
	return fmt.Sprintf("%d of %d (%.1f%%)", t.Passed, t.Passed+t.Failed, rate)
}

// days writes a duration in days, to one decimal.
func days(d time.Duration) string { return fmt.Sprintf("%.1f", d.Hours()/24) }

// top is the n most frequent findings of one kind, as table rows.
func top(findings map[finding]int, kind string, n int) [][]string {
	var names []string
	for f := range findings {
		if f.Kind == kind {
			names = append(names, f.Name)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := findings[finding{kind, names[i]}], findings[finding{kind, names[j]}]
		return a > b || a == b && names[i] < names[j]
	})
	var rows [][]string
	for _, name := range names[:min(n, len(names))] {
		rows = append(rows, []string{"`" + name + "`", fmt.Sprint(findings[finding{kind, name}])})
	}
	return rows
}

// render writes the report.
func render(r *report) string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	day := func(t time.Time) string { return t.UTC().Format("2006-01-02") }

	p("# AI developer quality report\n\n")
	p("How the pull requests opened by this repository's AI developer fared\n")
	p("against its checks. Written by `scripts/assessor`; every number below is\n")
	p("computed from the GitHub API and the git history by the queries at the\n")
	p("end, and nothing is estimated.\n\n")
	p("- History and guards read at commit `%s`.\n", r.Commit)
	p("- Pull requests opened %s to %s.\n\n", day(r.From), day(r.To))

	p("## What was measured\n\n")
	measuredAll := tally{}
	for _, m := range r.Months {
		measuredAll.Passed += m.All.Passed + m.All.Failed + m.All.Unmeasured
	}
	p("%s", table([]string{"Pull requests", "Count"}, [][]string{
		{"In the repository", fmt.Sprint(r.Pulls)},
		{"Opened by the AI developer's account", fmt.Sprint(r.AgentPulls)},
		{"Left out: no CI run on the branch while it was open", fmt.Sprint(r.NoRun)},
		{"With a first push that CI ran on", fmt.Sprint(measuredAll.Passed)},
	}))
	p("\nPull requests opened by any other account are left out: the history\n")
	p("cannot show who wrote them.\n\n")

	p("## 1. First-pass CI success\n\n")
	p("The first push of a pull request is the commit of the first CI run on\n")
	p("its branch. It passes when every check on that commit succeeded at its\n")
	p("first attempt. A first push whose checks were cancelled or never\n")
	p("finished is left out of the rate and counted in the last column.\n\n")
	p("`%s` is a required check that fails until a person acknowledges\n", r.Options.Gate)
	p("the change, so no author can pass it at a first push. The second and\n")
	p("fourth columns leave it out.\n\n")
	var rows [][]string
	var total month
	for _, m := range r.Months {
		if m.All.Passed+m.All.Failed+m.All.Unmeasured == 0 {
			continue
		}
		rows = append(rows, []string{m.Name, share(m.All), share(m.AllButGate),
			share(m.Required), share(m.RequiredNoGate), fmt.Sprint(m.AllButGate.Unmeasured)})
		for _, pair := range []struct{ to, from *tally }{
			{&total.All, &m.All}, {&total.AllButGate, &m.AllButGate},
			{&total.Required, &m.Required}, {&total.RequiredNoGate, &m.RequiredNoGate},
		} {
			pair.to.Passed += pair.from.Passed
			pair.to.Failed += pair.from.Failed
			pair.to.Unmeasured += pair.from.Unmeasured
		}
	}
	rows = append(rows, []string{"All", share(total.All), share(total.AllButGate),
		share(total.Required), share(total.RequiredNoGate), fmt.Sprint(total.AllButGate.Unmeasured)})
	p("%s", table([]string{"Opened", "Every check", "Every check but the gate",
		"Required checks", "Required but the gate", "Left out"}, rows))
	span := tally{}
	for _, m := range r.Months {
		span.Passed += m.SameSpan.Passed
		span.Failed += m.SameSpan.Failed
	}
	p("\nOver the same pull requests as the last two columns, every check but\n")
	p("the gate passed %s.\n", share(span))
	p("\nThe required columns start on %s, the day the repository first\n", day(r.RequiredFrom))
	p("declared its required checks in its own code. Before that day the set\n")
	p("is not on record, so those pull requests are in the first two columns\n")
	p("only.\n\n")

	p("## 2. Failures by category\n\n")
	p("Every failed job of every pull-request run on one of the AI\n")
	p("developer's branches: %d failed jobs in %d failed runs. A further %d\n",
		r.FailedJobs, r.FailedRuns, r.GateEcho)
	p("failures of the job that only waits on the other lanes are left out,\n")
	p("because each repeats a failure already counted.\n\n")
	var families []string
	for name := range r.Families {
		families = append(families, name)
	}
	sort.Slice(families, func(i, j int) bool {
		a, b := r.Families[families[i]], r.Families[families[j]]
		return a > b || a == b && families[i] < families[j]
	})
	rows = nil
	for _, name := range families {
		rows = append(rows, []string{name, fmt.Sprint(r.Families[name]),
			fmt.Sprintf("%.1f%%", 100*float64(r.Families[name])/float64(r.FailedJobs))})
	}
	p("%s", table([]string{"Kind of check", "Failed jobs", "Share"}, rows))
	p("\nWhat the logs of those jobs name, counted once per failed job. %d\n", r.LogsGone)
	p("logs were no longer kept by GitHub and are not counted.\n\n")
	for _, kind := range []struct {
		Name, Head string
		N          int
	}{
		{"Go test", "Go test or guard", 15}, {"Conftest rule", "Conftest rule", 10},
		{"Semgrep registry rule", "Semgrep registry rule", 10}, {"pre-commit hook", "Formatting hook", 5},
	} {
		if rows := top(r.Findings, kind.Name, kind.N); len(rows) > 0 {
			p("%s\n", table([]string{kind.Head, "Failed jobs"}, rows))
		}
	}

	p("## 3. Repeat failures\n\n")
	p("A guard here is a Go test under `%s` or a Conftest rule under\n", r.Options.Guards)
	p("`%s` whose own comment cites an issue. Its incident is\n", r.Options.Rules)
	p("the newest issue it cites that was opened before the guard merged. A\n")
	p("repeat is a failed job, after the guard merged, whose log names the\n")
	p("guard: the same mistake made again and stopped before it merged.\n\n")
	p("A pull request that rewrites the checks can trip a guard without making\n")
	p("its mistake, so the rate is given as a range. The upper bound counts\n")
	p("every pull request opened after the guard merged. The lower bound\n")
	p("counts only those that changed no file under `%s` or\n", r.Options.Guards)
	p("`%s`.\n\n", path.Dir(r.Options.Rules))
	repeated, repeats, plain, plainJobs := 0, 0, 0, 0
	var leads []time.Duration
	for _, inc := range r.Incidents {
		if inc.Repeats > 0 {
			repeated++
			repeats += inc.Repeats
		}
		if inc.Plain > 0 {
			plain++
			plainJobs += inc.Plain
		}
		leads = append(leads, inc.Lead)
	}
	rows = [][]string{
		{"Guards whose comment cites an issue or pull request", fmt.Sprint(r.Guards)},
		{"Of those, paired with an issue opened before the guard", fmt.Sprint(len(r.Incidents))},
		{"Upper bound: paired guards that later failed a pull request", fmt.Sprint(repeated)},
		{"Failed jobs they account for", fmt.Sprint(repeats)},
		{"Lower bound: of those, failed one that changed no check", fmt.Sprint(plain)},
		{"Failed jobs they account for", fmt.Sprint(plainJobs)},
	}
	if n := len(r.Incidents); n > 0 {
		rows = append(rows, []string{"Repeat-failure rate",
			fmt.Sprintf("%.1f%% to %.1f%% (%d to %d of %d)", 100*float64(plain)/float64(n),
				100*float64(repeated)/float64(n), plain, repeated, n)})
	}
	p("%s", table([]string{"Measure", "Value"}, rows))
	sorted := append([]incident(nil), r.Incidents...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Repeats > sorted[j].Repeats })
	rows = nil
	for _, inc := range sorted[:min(10, len(sorted))] {
		if inc.Repeats == 0 {
			break
		}
		rows = append(rows, []string{"`" + inc.Guard.Name + "`", fmt.Sprintf("#%d", inc.Issue),
			day(inc.Guard.Added), fmt.Sprint(inc.Repeats), fmt.Sprint(inc.Plain), day(inc.LastSeen)})
	}
	if len(rows) > 0 {
		p("\n%s", table([]string{"Guard", "Incident", "Guard merged", "Upper", "Lower", "Last"}, rows))
	}
	p("\nA repeat that no guard stopped cannot be counted from this data.\n\n")

	p("## 4. Before and after edits to `%s`\n\n", r.Options.Guidance)
	p("%d commits changed the file. For each, the first-pass rate (every\n", r.Edits)
	p("check but the gate) of the %d measured pull requests opened before it\n", r.Options.Window)
	p("is compared with the %d opened after. %d edits have that many on\n", r.Options.Window, r.Compared)
	p("both sides.\n\n")
	rows = [][]string{
		{"Edits followed by a higher rate", fmt.Sprint(r.Better)},
		{"Edits followed by a lower rate", fmt.Sprint(r.Worse)},
		{"Edits followed by the same rate", fmt.Sprint(r.Same)},
	}
	if n := len(r.Changes); n > 0 {
		rows = append(rows, []string{"Median change, percentage points", fmt.Sprintf("%+.1f", r.Changes[n/2])})
	}
	p("%s\n", table([]string{"Measure", "Value"}, rows))
	rows = nil
	for _, m := range r.Months {
		rows = append(rows, []string{m.Name, fmt.Sprint(m.Edits), share(m.AllButGate), fmt.Sprint(m.Repeats)})
	}
	p("%s", table([]string{"Month", "Edits", "First-pass, every check but the gate", "Repeats stopped"}, rows))
	p("\nThis shows what happened around each edit, not what the edit caused.\n")
	p("Guards, lanes and the kind of work changed in the same weeks.\n\n")

	p("## 5. From incident to guard\n\n")
	p("For the %d paired guards of section 3: the time from the issue being\n", len(r.Incidents))
	p("opened to the guard's commit landing on the main branch.\n\n")
	buckets := [3]int{}
	longest := time.Duration(0)
	for _, d := range leads {
		switch {
		case d < 24*time.Hour:
			buckets[0]++
		case d < 7*24*time.Hour:
			buckets[1]++
		default:
			buckets[2]++
		}
		longest = max(longest, d)
	}
	p("%s", table([]string{"Measure", "Value"}, [][]string{
		{"Median, days", days(median(leads))},
		{"Longest, days", days(longest)},
		{"Guard landed within a day", fmt.Sprint(buckets[0])},
		{"Within a week", fmt.Sprint(buckets[1])},
		{"Later", fmt.Sprint(buckets[2])},
	}))

	b.WriteString(limits)
	return b.String()
}

// limits is what the report cannot say, and the queries behind what it does.
const limits = `
## Limits

- **Authorship.** Only pull requests opened by the AI developer's own account
  are measured. Earlier ones were opened under a person's account and are
  left out, whoever wrote them.
- **Required checks before they were declared in code** are not on record,
  and GitHub does not give the history of a ruleset to this account. Those
  pull requests are measured against every check, not against a required set.
- **A first push replaced before its checks finished** has no answer and is
  left out. Its checks show as cancelled.
- **Job logs** are kept by GitHub for a limited time. A failed job whose log
  is gone is counted by its name and contributes no test or rule name.
- **Renamed guards.** A guard is dated from the first commit that holds its
  present name. One that was renamed, or moved from Go to a rule, is dated
  from the move, and failures under its old name are not joined to it.
- **Semgrep rules of the estate's own** are not in section 3: their failures
  reach the logs under the Go test that runs them, not under a rule name.
- **A guard's comment may cite an issue that is not the incident behind it.**
  The pairing is by the number in the comment and nothing else.
- **The repeat rate is a range, not a figure.** The lower bound leaves out
  every pull request that changed a guard or a rule, which here is a large
  share of them, so it says little. The upper bound counts a pull request
  rewriting the checks as a repeat. The true rate is between them.
- **Cause.** Section 4 compares rates around an edit. It cannot separate the
  edit from everything else that changed.

## Queries used

GitHub REST API, under ` + "`/repos/<owner>/<repo>`" + `:

` + "```text" + `
GET /pulls?state=all&sort=created&direction=asc     every pull request
GET /actions/runs                                   every workflow run
GET /commits/<sha>/check-runs?filter=all            checks of a first push
GET /actions/runs/<id>/jobs?filter=all              jobs of a failed run
GET /actions/jobs/<id>/logs                         log of a failed job
GET /issues/<n>                                     when an issue was opened
` + "```" + `

Git, at the commit named at the top:

` + "```text" + `
git log --format='%H %cI' --reverse <rev>
git grep -h -A80 -e 'required_checks = \[' <commit> -- '*.tf'
git ls-tree -r --name-only <rev> -- <guards> <rules>
git show <rev>:<file>
git log --format=%cI --reverse -S'func <Test>(' <rev> -- <guards>
git log --format=%cI --reverse --diff-filter=A <rev> -- <rule file>
git log --format=%cI --reverse <rev> -- <guidance file>
` + "```" + `

## Reproduce

` + "```sh" + `
task quality-report AGENT=<login>
` + "```" + `

The task builds ` + "`scripts/assessor`" + ` and runs it with a read-only token. The
numbers move as pull requests are opened; the commit and the dates at the
top say what a given copy was computed from.
`
