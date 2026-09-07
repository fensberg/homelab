package phases

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"

	"homelab/contractor/internal/run"
)

// Plan shows what a converge would do to an estate that already exists,
// without doing any of it.
//
// It is the half of the review a pull request could not give. Merging a change
// to management/ used to mean approving a diff of HCL and finding out what it
// meant afterwards; this answers "how does the estate change" before the merge
// rather than after it.
//
// # Why it reports structure and never values
//
// A plan holds every attribute of every resource it touches. This repository
// keeps hostnames, addresses and credentials out of git deliberately - the
// config template shows the shape of the estate without revealing what or
// where anything is - and the repository is public, which makes an Actions job
// summary and a pull request comment world-readable by anyone.
//
// So this reports addresses and actions and nothing else. It is the same line
// check-inventory already draws between "the reference resolves" and "here is what
// it resolved to", for the same reason: the output is most useful exactly when
// somebody wants to paste it somewhere.
func Plan(ctx *run.Context) error {
	run.WritePhase("Plan", "Show what a converge would change, without changing it.")

	// The saved plan file holds the values this summary refuses to print, so
	// it never outlives the phase that made it. Sterilize lists it too, for
	// the run that dies before reaching this line.
	defer func() { _ = os.Remove(ctx.TofuPlanFile) }()

	run.Info("planning")
	// tofu's own plan output is captured and discarded rather than streamed.
	//
	// It is redundant - the summary below is built from `tofu show -json` of
	// the same plan file - and it is a leak. Resource addresses are not the
	// safe half they were assumed to be: `for_each` keys come from the config,
	// so a plan prints things like the hypervisor's name as a map key and the
	// cluster's name as a data source id, both of which are vault values. A
	// converge runs in a public repository's Actions log and its output is
	// pasted into a pull request comment.
	//
	// TofuApply already had this discipline, and its comment records a
	// converge printing the site's real name inside a resource description.
	// Plan never got it, so the same class of leak went out through the
	// quieter path.
	//
	// Errors are unaffected: tofu writes diagnostics to stderr, which is not
	// captured here, so a failing plan still says why.
	if _, err := run.CmdOutput(ctx.ClusterDir, "tofu",
		"plan", "-input=false", "-out="+ctx.TofuPlanFile,
	); err != nil {
		return fmt.Errorf("tofu plan: %w", err)
	}

	raw, err := run.CmdOutput(ctx.ClusterDir, "tofu", "show", "-json", ctx.TofuPlanFile)
	if err != nil {
		return fmt.Errorf("reading the plan back: %w", err)
	}

	summary, err := summarisePlan([]byte(raw))
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Println(summary)

	if ctx.CommentOut != "" {
		if err := os.WriteFile(ctx.CommentOut, []byte(commentBody(ctx.Site, summary, plannedCommit())), 0o644); err != nil {
			return fmt.Errorf("writing the comment body: %w", err)
		}
	}
	return nil
}

// commentBody renders the pull request comment.
//
// Written here rather than assembled in the workflow, for the reason
// sensitive-paths.yml already records about its own comment: copy that needs a
// workflow edit to fix is copy that stays wrong, because the agent cannot edit
// workflows and the human should not have to for a wording change.
//
// It is deliberately plain. A heading naming the site, the change, and nothing
// else - no greeting, no signature, and no restatement of what the output is,
// because a reader can see what it is. The marker is what lets a later run
// update this comment instead of adding another one.
func commentBody(site, summary, commit string) string {
	// The commit is what makes a stale comment visible.
	//
	// A later run replaces this comment in place, so its contents are only ever
	// as current as the last plan that succeeded. When the plan lane cannot run
	// - no runner, which is the estate's normal state while it is being rebuilt
	// - the previous comment simply stays, describing a change that is no
	// longer the one being proposed. Push 3 -> 5, then 5 -> 17, and a reviewer
	// reads "2 to add" and approves fourteen machines.
	//
	// Nothing in the body distinguished a fresh comment from a stale one: no
	// commit, no timestamp, and GitHub dismisses stale reviews on a push but
	// never comments. So it says which commit it describes, and a reader can
	// compare that against the pull request in one glance.
	provenance := ""
	if commit != "" {
		provenance = fmt.Sprintf("\nPlanned against `%s`, as it would be once merged.\n", commit)
	}
	return fmt.Sprintf("%s\n## Plan — %s\n%s\n```text\n%s\n```\n",
		commentMarker(site), site, provenance, strings.TrimRight(summary, "\n"))
}

// plannedCommit is the commit this plan describes, as a reader would recognise
// it.
//
// THE POINT IS RECOGNISABILITY, NOT PRECISION. On a pull request the workspace
// is a merge commit GitHub synthesises for the run. Planning against it is
// correct - it is the tree that will exist after the merge - but naming it is
// useless, because it appears in no branch, in no clone, and nowhere in the
// pull request's own list of commits. A reviewer given that SHA cannot look it
// up, which is exactly what happened: a plan comment said "Planned against
// ea516dc" and the operator reasonably replied that they had no idea what that
// was (#320).
//
// So this reports the BRANCH HEAD and says the plan covers it as merged. The
// SHA is then one a reader can click.
//
// WHY THE EVENT PAYLOAD RATHER THAN GIT. The previous version asked git for
// HEAD^2 - the merge commit's second parent, which is the branch head - and
// fell back to HEAD. That was right in principle and dead in practice: the
// plan job checks out at the default depth of one, so the merge commit's
// parents are not in the clone, HEAD^2 fails, and it fell back to naming the
// merge commit. A fallback that silently produces a plausible wrong answer is
// worse than no fallback, and this one had been producing it on every plan.
//
// The event payload is authoritative rather than inferred, and it needs no
// change to the workflow - which matters because this repository's agent
// cannot edit one, so a fix that required deepening the checkout would have
// been a fix waiting on a human.
func plannedCommit() string {
	if sha := headFromEvent(os.Getenv("GITHUB_EVENT_PATH")); sha != "" {
		return sha
	}
	// A full clone - a workstation, or any job that fetches depth 0 - can
	// still answer this from topology.
	if head, err := run.CmdOutputQuiet(".", "git", "rev-parse", "--short", "HEAD^2"); err == nil {
		if s := strings.TrimSpace(head); s != "" {
			return s
		}
	}
	// Not a pull request at all: HEAD is already the commit worth naming. On
	// a pull request it is the merge commit, and naming that is the bug above,
	// so this deliberately does not run there.
	if os.Getenv("GITHUB_EVENT_NAME") == "pull_request" {
		return ""
	}
	if head, err := run.CmdOutputQuiet(".", "git", "rev-parse", "--short", "HEAD"); err == nil {
		return strings.TrimSpace(head)
	}
	return ""
}

// headFromEvent reads pull_request.head.sha out of the Actions event payload
// and shortens it the way git would.
//
// Split out so it can be tested against a payload file without an Actions
// runner, a pull request or a repository - the same reason summarisePlan is
// separate from Plan.
func headFromEvent(path string) string {
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var event struct {
		PullRequest struct {
			Head struct {
				SHA string `json:"sha"`
			} `json:"head"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return ""
	}
	sha := strings.TrimSpace(event.PullRequest.Head.SHA)
	// Anything that is not a full hex object name is not a commit, and
	// truncating one would produce a SHA-shaped string that resolves to
	// nothing - the failure this whole function exists to stop.
	if len(sha) != 40 || strings.Trim(sha, "0123456789abcdef") != "" {
		return ""
	}
	return sha[:7]
}

// commentMarker identifies this comment so a later run can find and replace
// it. Per site, because a repository with two sites plans both on one pull
// request and one must not overwrite the other.
func commentMarker(site string) string {
	return "<!-- plan:" + site + " -->"
}

type planChange struct {
	Address string `json:"address"`
	Mode    string `json:"mode"`
	Change  struct {
		Actions []string `json:"actions"`
	} `json:"change"`
}

type outputChange struct {
	Actions []string `json:"actions"`
}

// planDoc is the part of a tofu plan this summary reads.
//
// FormatVersion is not decoration. Every field below is optional in JSON - a
// document with none of them unmarshals cleanly into an empty struct, and an
// empty struct used to render "No changes. The estate already matches the
// config.", which is a positive claim about reality made from having read
// nothing. A plan document always carries a format version, so requiring it
// is what separates "this plan holds no changes" from "this is not a plan".
//
// There is deliberately no third status for the second case. A plan that
// examined nothing means the tool is broken, not that the estate is quiet, and
// inventing a calm-looking way to say so would put the reassuring words in
// front of the reader at exactly the wrong moment. It is an error.
type planDoc struct {
	FormatVersion   string                  `json:"format_version"`
	ResourceChanges []planChange            `json:"resource_changes"`
	OutputChanges   map[string]outputChange `json:"output_changes"`
}

// summarisePlan renders a plan as addresses and verbs.
//
// Split from Plan so the property that matters - that no attribute value
// reaches the output - is testable against a plan full of them, without tofu
// or an estate.
func summarisePlan(raw []byte) (string, error) {
	var doc planDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("this is not a tofu plan in JSON form: %w", err)
	}
	if doc.FormatVersion == "" {
		return "", fmt.Errorf("this JSON has no plan format version, so it is not a plan and nothing here examined the estate")
	}

	type row struct{ address, verb string }
	var rows []row
	counts := map[string]int{}

	for _, c := range doc.ResourceChanges {
		verb := classify(c.Change.Actions)
		// A data source is not a work. It is measured, and the estate is
		// unchanged by measuring it - so it gets the verb this estate already
		// uses for reading the ground as it is rather than trusting the
		// drawings, and it stays out of the works count below.
		//
		// This has to come BEFORE the empty-verb check, not after it. tofu's
		// action for a deferred data source is "read", classify has no case
		// for it, and its default returns the same empty string a no-op does -
		// so the read was being discarded as noise. That was half of #320: a
		// data source whose inputs changed is the only signal that something
		// like the talosconfig is about to be rebuilt.
		if c.Mode == "data" && slices.Contains(c.Change.Actions, "read") {
			verb = "survey"
		}
		if verb == "" {
			continue // no-op: noise in a review, not information
		}
		rows = append(rows, row{redactKeys(c.Address), verb})
		counts[verb]++
	}

	// Outputs, which used to be invisible entirely.
	//
	// A change confined to outputs is a real change to what this estate hands
	// out - the talosconfig and the kubeconfig are both outputs, and both are
	// credentials somebody uses. Reading only resource_changes meant a commit
	// that rewrote one of them summarised as "No changes", which is the plan
	// comment supplying confidence rather than information (#320).
	//
	// Names only, and never a before or an after: an output can BE a secret,
	// so the rule that governs resource attributes governs these with more
	// force rather than less. The name is safe because it is a static
	// identifier declared in HCL, unlike a for_each key, which redactKeys
	// exists to strip.
	outputNames := make([]string, 0, len(doc.OutputChanges))
	for name := range doc.OutputChanges {
		outputNames = append(outputNames, name)
	}
	sort.Strings(outputNames)
	for _, name := range outputNames {
		verb := classify(doc.OutputChanges[name].Actions)
		if verb == "" {
			continue
		}
		rows = append(rows, row{"output." + name, verb})
		counts["output"]++
	}

	if len(rows) == 0 {
		return "  No changes. The estate already matches the config.", nil
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].address < rows[j].address })

	var b strings.Builder
	// Longest verb first so the addresses line up without tabwriter.
	width := 0
	for _, r := range rows {
		if len(r.verb) > width {
			width = len(r.verb)
		}
	}
	for _, r := range rows {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, r.verb, r.address)
	}

	// The works, counted. Always all four, including the zeroes: a reviewer
	// reads this line to find the destroy count, and a line whose shape
	// changes with its contents is one you have to read rather than glance at.
	b.WriteString("\n  ")
	var parts []string
	for _, v := range []struct{ verb, label string }{
		{"add", "to add"}, {"change", "to change"},
		{"replace", "to replace"}, {"destroy", "to destroy"},
	} {
		parts = append(parts, fmt.Sprintf("%d %s", counts[v.verb], v.label))
	}
	b.WriteString(strings.Join(parts, ", "))
	b.WriteString("\n")

	// Everything that is not a work goes on its own line, and only when there
	// is any. These do not belong in the count above - an output changing is
	// not a machine changing, and folding them together would inflate the one
	// number a reviewer is scanning for.
	var aside []string
	if counts["output"] > 0 {
		aside = append(aside, fmt.Sprintf("%d output(s) to change", counts["output"]))
	}
	if counts["survey"] > 0 {
		aside = append(aside, fmt.Sprintf("%d to survey", counts["survey"]))
	}
	if len(aside) > 0 {
		fmt.Fprintf(&b, "  %s\n", strings.Join(aside, ", "))
	}

	// The one line a reviewer must not skim past. A converge that destroys is
	// almost always either deliberate and understood, or a mistake nobody
	// spotted - and the two look identical in a count.
	if counts["destroy"] > 0 || counts["replace"] > 0 {
		b.WriteString("\n  THIS PLAN DESTROYS OR REPLACES RESOURCES. Read every line above before merging.\n")
	}
	return b.String(), nil
}

// classify collapses tofu's action list into one verb. A delete paired with a
// create is a replacement, which is a different risk from either alone.
func classify(actions []string) string {
	switch {
	case slices.Contains(actions, "no-op"):
		return ""
	case len(actions) > 1:
		return "replace"
	case slices.Contains(actions, "create"):
		return "add"
	case slices.Contains(actions, "update"):
		return "change"
	case slices.Contains(actions, "delete"):
		return "destroy"
	default:
		return ""
	}
}

// forEachKey matches the bracketed key in a resource address.
var forEachKey = regexp.MustCompile(`\["([^"]*)"\]`)

// numericKey is a key that cannot carry a proper noun.
var numericKey = regexp.MustCompile(`^[0-9]+$`)

// redactKeys removes `for_each` keys that can carry a vault value.
//
// Resource addresses were treated as the safe half of a plan - "addresses and
// verbs, never a value" - and they are not. A `for_each` over the config's
// hypervisor map keys the resource by the hypervisor's real name, so a plan
// prints it in an address without printing any attribute at all. That reached
// a public Actions log and a pull request comment before anybody noticed,
// because everything watching for leaks was watching the values.
//
// Numeric keys are kept. They come from octets and node numbering, carry no
// proper noun, and are the difference between "a control-plane VM is being
// replaced" and knowing which one - which is exactly what a reviewer needs
// when the plan says something is being destroyed.
func redactKeys(address string) string {
	return forEachKey.ReplaceAllStringFunc(address, func(m string) string {
		key := forEachKey.FindStringSubmatch(m)[1]
		if numericKey.MatchString(key) {
			return m
		}
		return `["<redacted>"]`
	})
}
