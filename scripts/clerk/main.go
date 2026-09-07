// Command clerk is the outside party: it reads this repository without its
// context, and it can stop nothing.
//
// A clerk of works inspects on the client's behalf, separately from the
// contractor. The name is a real role rather than a label, and the separation
// is the point: the agent that writes the code here holds years of decisions
// about why it looks the way it does, and that context is exactly what stops
// it seeing the artefact as a stranger would.
//
//	preflight  prove the credentials work, and report what they actually carry
//	account    write a plain account of what some code does, having read only it
//	review     post that account on a pull request, as a comment and never more
//
// The inspector is deliberately a separate program. It sits inside the merge
// path, its output attaches to a gate, and it must degrade to a static reason
// when the vendor is unreachable. This one has no lever at all. Bundling them
// would put a defect in an outside party's issue-filing inside the binary
// standing in front of a merge.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	githubAPI = "https://api.github.com"
	modelAPI  = "https://generativelanguage.googleapis.com"

	// Bounded, because this reaches a metered vendor from a scheduled job and
	// the building code governs that shape. The free tier makes the worst case
	// a rate limit rather than an invoice; these bounds are what keep it a
	// short one.
	askAttempts = 3
	askBackoff  = 5 * time.Second
	askTimeout  = 120 * time.Second

	// Conservative against the free tier's 250,000 tokens a minute. Two passes
	// over a directory beat one prompt truncated mid-function.
	promptBudget = 400_000
)

type verb struct {
	name string
	what string
	run  func(args []string) int
}

func main() {
	verbs := []verb{
		{"snag", "walk the work and list what is unsound or does not match what was written about it", snagVerb},
		{"handover", "read it as a stranger who has just cloned it, and list what would stop them", handoverVerb},
	}

	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, "clerk reads this repository as a stranger would.\n\nusage: clerk <verb> [flags]\n\n")
		for _, v := range verbs {
			fmt.Fprintf(os.Stderr, "  %-10s %s\n", v.name, v.what)
		}
		os.Exit(2)
	}

	for _, v := range verbs {
		if v.name == os.Args[1] {
			os.Exit(v.run(os.Args[2:]))
		}
	}
	fmt.Fprintf(os.Stderr, "clerk: no such verb %q\n", os.Args[1])
	os.Exit(2)
}

// need reports a missing input by name rather than failing later and deeper.
//
// The estate's own lesson, learned expensively on a converge that spent five
// minutes retrying a binary that was never installed and then blamed etcd: an
// input that is absent should be named at the start, not discovered in the
// middle of the work it was needed for.
func need(names ...string) (map[string]string, error) {
	got := map[string]string{}
	var missing []string
	for _, n := range names {
		v := strings.TrimSpace(os.Getenv(n))
		if v == "" {
			missing = append(missing, n)
			continue
		}
		got[n] = v
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("missing %s", strings.Join(missing, ", "))
	}
	return got, nil
}

func newAsker(key, model string) *asker {
	return &asker{
		endpoint: modelAPI,
		model:    model,
		key:      key,
		http:     &http.Client{Timeout: askTimeout},
		attempts: askAttempts,
		backoff:  askBackoff,
	}
}

// walk is one verb: read a slice of the repository, ask about it, and report
// findings as SARIF.
//
// SARIF rather than prose in a comment. A snagging list is discrete items each
// pinned to a place; prose in one comment cannot be dismissed item by item,
// cannot close itself when the defect goes, and cannot be counted. The
// dismissal reasons are what turn this epoch's acceptance test from a
// judgement into a number: "false positive" against "won't fix" is exactly the
// split between the clerk being wrong and the clerk being right and overruled.
func walk(name string, args []string, ask func(*asker, *bundle) ([]snag, string, error)) int {
	fs := flag.NewFlagSet("clerk "+name, flag.ContinueOnError)
	root := fs.String("root", ".", "repository root")
	out := fs.String("out", "", "write the SARIF report here instead of stdout")
	model := fs.String("model", "", "override the pinned model")
	pr := fs.Int("pr", 0, "also post a short note on this pull request")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	paths := fs.Args()
	if len(paths) == 0 {
		fmt.Fprintf(os.Stderr, "clerk %s: name at least one path to read\n", name)
		return 2
	}

	env, err := need("CLERK_BOT_LLM_KEY")
	if err != nil {
		fmt.Fprintln(os.Stderr, "clerk:", err)
		return 2
	}
	chosen := *model
	if chosen == "" {
		pinned, err := need("CLERK_MODEL_VERSION")
		if err != nil {
			fmt.Fprintln(os.Stderr, "clerk:", err)
			return 2
		}
		chosen = pinned["CLERK_MODEL_VERSION"]
	}

	files, err := tracked(*root, paths)
	if err != nil {
		fmt.Fprintln(os.Stderr, "clerk:", err)
		return 1
	}
	b, err := read(*root, files, promptBudget)
	if err != nil {
		fmt.Fprintln(os.Stderr, "clerk:", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "clerk %s: read %d of %d tracked files\n", name, len(b.included), len(files))

	found, caveat, err := ask(newAsker(env["CLERK_BOT_LLM_KEY"], chosen), b)
	if err != nil {
		fmt.Fprintln(os.Stderr, "clerk:", err)
		return 1
	}
	if caveat != "" {
		fmt.Fprintf(os.Stderr, "clerk %s: %s\n", name, caveat)
	}

	kept, dropped := keep(found, b.lines)
	// Said out loud, always. "Nothing found" and "eleven findings discarded
	// because none of them could be checked" are different facts, and only one
	// of them is reassuring.
	fmt.Fprintf(os.Stderr, "clerk %s: %d snag(s), %d discarded as uncheckable\n", name, len(kept), len(dropped))
	for _, d := range dropped {
		fmt.Fprintf(os.Stderr, "  discarded: %s\n", d)
	}

	report, err := sarif(kept)
	if err != nil {
		fmt.Fprintln(os.Stderr, "clerk:", err)
		return 1
	}
	if *out == "" {
		fmt.Println(string(report))
	} else if err := os.WriteFile(*out, report, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "clerk:", err)
		return 1
	}

	// Which of the findings the pull request will actually display. Only the
	// ones it will not are worth a comment; see note.
	var unseen []snag
	if *pr != 0 && len(kept) > 0 {
		shown, err := shownLines(*pr)
		switch {
		case err != nil:
			// Cannot tell, so assume the worst rather than the convenient
			// thing: a comment too many costs a line, and being wrong the
			// other way means findings nobody ever sees.
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
		fmt.Fprintf(os.Stderr, "clerk %s: %d of %d finding(s) fall outside the diff\n", name, len(unseen), len(kept))
	}

	// An empty note means there is nothing to say that the alerts do not
	// already say, and silence is the answer rather than a receipt.
	if body := note(name, kept, unseen, dropped, caveat); *pr != 0 && body != "" {
		if code := post(*pr, body); code != 0 {
			return code
		}
	}
	return 0
}

// snagVerb walks the work twice, and the order is the whole point.
//
// The first pass sees the code with every comment blanked out, so it cannot be
// told what the code is for while deciding whether it holds together. The
// second pass sees only that pass's account and the commentary - never the
// code - so it cannot read a claim and then go looking for it.
func snagVerb(args []string) int {
	return walk("snag", args, snagPass)
}

// snagPass is the two-pass walk itself, named rather than inline so a test can
// drive it with a bundle and an asker that fails if it is reached.
func snagPass(a *asker, b *bundle) ([]snag, string, error) {
	// A change carrying no code has nothing to write an account of.
	//
	// This is the mirror of the prose guard below and it was missing, which
	// #241 found: five epoch records and no code at all. `split` sends Markdown
	// wholly to the prose side, so `b.code` was empty, the first pass was asked
	// about nothing, and it answered - correctly - with no account. parseBlind
	// then reported that as a failure, so a pull request there was never
	// anything to read failed the lane rather than passing it.
	//
	// Returning before the ask also spends no request. The clerk's models are
	// on a free tier measured in tens of calls a day, so a call that cannot
	// produce an answer is a real cost rather than an untidiness.
	if strings.TrimSpace(b.code) == "" {
		return nil, "there is no code in this change, so nothing was reviewed", nil
	}

	blind, err := a.ask(blindPrompt + "\n" + b.code)
	if err != nil {
		return nil, "", err
	}
	account, unsound, err := parseBlind(blind)
	if err != nil {
		return nil, "", err
	}

	if strings.TrimSpace(b.prose) == "" {
		return unsound, "nothing was written about these files, so only the soundness pass ran", nil
	}

	compared, err := a.ask(comparePrompt + "\n=== the account ===\n" + account + "\n" + b.prose)
	if err != nil {
		return nil, "", err
	}
	disagrees, err := parse(compared)
	if err != nil {
		return nil, "", err
	}
	return append(unsound, disagrees...), "", nil
}

func handoverVerb(args []string) int {
	return walk("handover", args, func(a *asker, b *bundle) ([]snag, string, error) {
		// A stranger sees everything, commentary included - that is the point
		// of the question. So this pass is given the file as written. There is
		// no empty-input case here the way there is in snagPass: read() already
		// refuses a bundle with no files, and prose alone is a perfectly good
		// subject for "could somebody else run this".
		answer, err := a.ask(handoverPrompt + "\n" + b.code + "\n" + b.prose)
		if err != nil {
			return nil, "", err
		}
		found, err := parse(answer)
		return found, "", err
	})
}

// note is what the pull request is told, and usually it is nothing.
//
// THE RULE, which is the operator's and was stated plainly: zero findings gets
// a comment, more than zero gets silence.
//
// It took two goes to implement, and the way it was got wrong is worth
// keeping. Told "N > 0 hide", the first attempt kept a comment for N > 0 and
// merely stopped it restating the findings - and wrote a long justification
// for the receipt into this very comment block. That is substituting a design
// argument for a decision somebody had already made. The findings are on the
// diff, on the line they are about, in a thread that can be replied to and
// dismissed; a comment beside them saying how many there are asks the reader
// to check that two counts agree and offers nothing else.
//
// Zero findings is the opposite case and must always speak. With no comment at
// all, a clerk that read the change and found it sound looks exactly like one
// that was skipped, and this estate refuses that everywhere else: "I was not
// given anything to look at" and "I looked and it is fine" are different facts
// and only one is reassuring. That is also why the caveat exists - a change
// carrying no readable code produces the same zero as a clean one.
//
// Two things survive into the N > 0 case, and neither is a receipt.
//
// A CAVEAT, alone and with no count. It says the reading itself was partial,
// which is a fact about the clerk rather than about the code, so no alert
// carries it and hiding it would lose it silently.
//
// FINDINGS THE PULL REQUEST WILL NOT SHOW. This is the qualification the rule
// needed and did not have, and #277 is what found it. The clerk reads changed
// files WHOLE, so it regularly finds things on lines the change never touched
// - and GitHub renders an alert on the pull request only when it falls inside
// the diff. Such a finding is uploaded, counted, opened as an alert, and
// displayed nowhere the reviewer is looking.
//
// While the clerk still posted a count that was survivable, because the count
// said "go and look". Without it the run is silent while holding findings,
// which is worse than the redundancy the rule was written to remove. So the
// comment comes back for exactly those, and says nothing about the rest: the
// alerts on changed lines speak for themselves, and repeating them is the
// thing that was wrong in the first place.
//
// An empty return means post nothing.
func note(name string, kept, unseen []snag, dropped []string, caveat string) string {
	if len(kept) == 0 {
		headline := "nothing to raise"
		if caveat != "" {
			headline = caveat
		}
		return fmt.Sprintf("**clerk %s** — %s. %d finding(s) discarded as uncheckable.\n\n"+
			"A second opinion from a reader with no context: it can approve nothing and block nothing.",
			name, headline, len(dropped))
	}

	// Findings on changed lines are already on the diff, and this says nothing
	// about them - not what they are, not how many, not that there were any.
	if len(unseen) == 0 && caveat == "" {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "**clerk %s**", name)
	if caveat != "" {
		fmt.Fprintf(&b, " — read with a caveat: %s", caveat)
	}
	b.WriteString("\n\n")
	if len(unseen) > 0 {
		b.WriteString("These are on lines this pull request does not show, so they are " +
			"alerts with nowhere to appear on the diff:\n\n")
		for _, s := range unseen {
			fmt.Fprintf(&b, "- `%s:%d` — %s\n", s.Path, s.Line, s.Message)
		}
		b.WriteString("\nAnything found on a changed line is inline on the diff and is not repeated here.")
	} else {
		b.WriteString("Anything found is on the diff.")
	}
	return b.String()
}

// shownLines asks GitHub which lines of which files this pull request displays.
//
// Same credential path as post, and a failure here is reported rather than
// fatal: not knowing which lines are shown is a reason to say more, never a
// reason to fail a reading that has already been done.
func shownLines(pr int) (map[string]map[int]bool, error) {
	env, err := need("CLERK_BOT_APP_ID", "CLERK_BOT_PRIVATE_KEY", "GITHUB_REPOSITORY")
	if err != nil {
		return nil, err
	}
	key, err := parseAppKey(env["CLERK_BOT_PRIVATE_KEY"])
	if err != nil {
		return nil, err
	}
	g, _, err := exchange(githubAPI, env["GITHUB_REPOSITORY"], env["CLERK_BOT_APP_ID"], key, &http.Client{Timeout: 30 * time.Second}, time.Now())
	if err != nil {
		return nil, err
	}
	return g.shown(pr)
}

// post puts a short note on a pull request, as a comment and never more.
func post(pr int, body string) int {
	env, err := need("CLERK_BOT_APP_ID", "CLERK_BOT_PRIVATE_KEY", "GITHUB_REPOSITORY")
	if err != nil {
		fmt.Fprintln(os.Stderr, "clerk:", err)
		return 2
	}
	key, err := parseAppKey(env["CLERK_BOT_PRIVATE_KEY"])
	if err != nil {
		fmt.Fprintln(os.Stderr, "clerk:", err)
		return 1
	}
	g, _, err := exchange(githubAPI, env["GITHUB_REPOSITORY"], env["CLERK_BOT_APP_ID"], key, &http.Client{Timeout: 30 * time.Second}, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "clerk:", err)
		return 1
	}
	if err := g.say(pr, body); err != nil {
		fmt.Fprintln(os.Stderr, "clerk:", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "clerk: commented on #%d\n", pr)
	return 0
}
