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
		{"audit", "write an account of what the given tracked files do", audit},
		{"handover", "read it as a stranger who has just cloned it", handover},
		{"preflight", "prove the credentials work and report what they carry", preflight},
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

// preflight answers "would a real run work", and says nothing that is a value.
//
// It reports the permissions the installation token actually carries, read
// from the API rather than from a settings page - those are different facts,
// and only one of them decides what this program can do. Its output is
// structure, never a value, because it lands in a public repository's log.
func preflight(args []string) int {
	env, err := need("CLERK_BOT_APP_ID", "CLERK_BOT_PRIVATE_KEY", "CLERK_BOT_LLM_KEY", "CLERK_MODEL_VERSION", "GITHUB_REPOSITORY")
	if err != nil {
		fmt.Fprintln(os.Stderr, "clerk preflight:", err)
		return 2
	}

	fmt.Println("## Clerk preflight")
	fmt.Println()

	key, err := parseAppKey(env["CLERK_BOT_PRIVATE_KEY"])
	if err != nil {
		fmt.Printf("- app key: FAILED - %v\n", err)
		return 1
	}
	fmt.Println("- app key: parsed")

	g, perms, err := exchange(githubAPI, env["GITHUB_REPOSITORY"], env["CLERK_BOT_APP_ID"], key, &http.Client{Timeout: 30 * time.Second}, time.Now())
	if err != nil {
		fmt.Printf("- installation token: FAILED - %v\n", err)
		return 1
	}
	fmt.Printf("- installation token: issued for %s\n", g.repo)

	names := make([]string, 0, len(perms))
	for k := range perms {
		names = append(names, k)
	}
	sort.Strings(names)
	fmt.Println("- permissions the token actually carries:")
	for _, n := range names {
		fmt.Printf("    %s: %s\n", n, perms[n])
	}
	if perms["pull_requests"] != "write" {
		fmt.Println("    NOTE: without pull_requests write the clerk cannot post a review at all.")
	}

	answer, err := newAsker(env["CLERK_BOT_LLM_KEY"], env["CLERK_MODEL_VERSION"]).ask("Reply with the single word: ok")
	if err != nil {
		fmt.Printf("- model %s: FAILED - %v\n", env["CLERK_MODEL_VERSION"], err)
		return 1
	}
	fmt.Printf("- model %s: answered %d characters\n", env["CLERK_MODEL_VERSION"], len(strings.TrimSpace(answer)))

	fmt.Println()
	fmt.Println("Everything the clerk needs is present.")
	return 0
}

// speak runs one verb: read the named tracked files, ask the question, and
// either print the answer or post it on a pull request.
//
// Posting is a flag rather than a verb of its own. Where the account goes is
// not a different job - a finding about one change belongs on that change and
// should die with it, and a finding about the estate belongs in an issue and
// should outlive it. Same reading either way.
func speak(name, prompt string, args []string) int {
	fs := flag.NewFlagSet("clerk "+name, flag.ContinueOnError)
	pr := fs.Int("pr", 0, "post on this pull request as a comment, instead of printing")
	root := fs.String("root", ".", "repository root")
	model := fs.String("model", "", "override the pinned model")
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
	built, included, err := gather(prompt, *root, files, promptBudget)
	if err != nil {
		fmt.Fprintln(os.Stderr, "clerk:", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "clerk %s: read %d of %d tracked files\n", name, len(included), len(files))

	text, err := newAsker(env["CLERK_BOT_LLM_KEY"], chosen).ask(built)
	if err != nil {
		fmt.Fprintln(os.Stderr, "clerk:", err)
		return 1
	}

	if *pr == 0 {
		fmt.Println(text)
		return 0
	}
	return post(*pr, name, text)
}

func audit(args []string) int    { return speak("audit", auditPrompt, args) }
func handover(args []string) int { return speak("handover", handoverPrompt, args) }

// post puts the account on a pull request, as a comment and never more.
func post(pr int, name, text string) int {
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

	body := "**clerk " + name + "** - an account written by a reader with no context, who never saw what we claim this does. " +
		"A second opinion, not a verdict: it can approve nothing and block nothing.\n\n---\n\n" + text
	if err := g.say(pr, body); err != nil {
		fmt.Fprintln(os.Stderr, "clerk:", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "clerk: commented on #%d\n", pr)
	return 0
}
