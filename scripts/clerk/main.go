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
func walk(name string, args []string, ask func(*asker, *bundle) ([]snag, error)) int {
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

	found, err := ask(newAsker(env["CLERK_BOT_LLM_KEY"], chosen), b)
	if err != nil {
		fmt.Fprintln(os.Stderr, "clerk:", err)
		return 1
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

	if *pr != 0 {
		note := fmt.Sprintf("**clerk %s** — %d snag(s) raised, %d discarded as uncheckable.\n\n"+
			"Each snag is an alert on the changed files, dismissable on its own. "+
			"This is a second opinion from a reader with no context: it can approve nothing and block nothing.",
			name, len(kept), len(dropped))
		if code := post(*pr, note); code != 0 {
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
	return walk("snag", args, func(a *asker, b *bundle) ([]snag, error) {
		blind, err := a.ask(blindPrompt + "\n" + b.code)
		if err != nil {
			return nil, err
		}
		account, unsound, err := parseBlind(blind)
		if err != nil {
			return nil, err
		}

		if strings.TrimSpace(b.prose) == "" {
			fmt.Fprintln(os.Stderr, "clerk snag: nothing was written about these files, so there is nothing to compare")
			return unsound, nil
		}

		compared, err := a.ask(comparePrompt + "\n=== the account ===\n" + account + "\n" + b.prose)
		if err != nil {
			return nil, err
		}
		disagrees, err := parse(compared)
		if err != nil {
			return nil, err
		}
		return append(unsound, disagrees...), nil
	})
}

func handoverVerb(args []string) int {
	return walk("handover", args, func(a *asker, b *bundle) ([]snag, error) {
		// A stranger sees everything, commentary included - that is the point
		// of the question. So this pass is given the file as written.
		answer, err := a.ask(handoverPrompt + "\n" + b.code + "\n" + b.prose)
		if err != nil {
			return nil, err
		}
		return parse(answer)
	})
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
