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
		if err := os.WriteFile(ctx.CommentOut, []byte(commentBody(ctx.Site, summary)), 0o644); err != nil {
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
func commentBody(site, summary string) string {
	return fmt.Sprintf("%s\n## Plan — %s\n\n```text\n%s\n```\n",
		commentMarker(site), site, strings.TrimRight(summary, "\n"))
}

// commentMarker identifies this comment so a later run can find and replace
// it. Per site, because a repository with two sites plans both on one pull
// request and one must not overwrite the other.
func commentMarker(site string) string {
	return "<!-- plan:" + site + " -->"
}

type planChange struct {
	Address string `json:"address"`
	Change  struct {
		Actions []string `json:"actions"`
	} `json:"change"`
}

type planDoc struct {
	ResourceChanges []planChange `json:"resource_changes"`
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

	type row struct{ address, verb string }
	var rows []row
	counts := map[string]int{}

	for _, c := range doc.ResourceChanges {
		verb := classify(c.Change.Actions)
		if verb == "" {
			continue // no-op: noise in a review, not information
		}
		rows = append(rows, row{redactKeys(c.Address), verb})
		counts[verb]++
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
