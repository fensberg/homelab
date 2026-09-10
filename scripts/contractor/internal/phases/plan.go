package phases

import (
	"bytes"
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
		// Top-level attributes only, and never their contents. See
		// changedAttributes for why the depth limit is the safety property
		// rather than an approximation.
		Before       map[string]json.RawMessage `json:"before"`
		After        map[string]json.RawMessage `json:"after"`
		AfterUnknown map[string]json.RawMessage `json:"after_unknown"`
		// Which attributes forced a replacement. The most valuable field in a
		// plan and the one nothing here was reading: "this machine is being
		// rebuilt" and "this machine is being rebuilt BECAUSE ITS DISK
		// CHANGED" are different decisions.
		ReplacePaths [][]json.RawMessage `json:"replace_paths"`
	} `json:"change"`
}

type outputChange struct {
	Actions         []string        `json:"actions"`
	BeforeSensitive json.RawMessage `json:"before_sensitive"`
	AfterSensitive  json.RawMessage `json:"after_sensitive"`
}

// sensitive reports whether tofu marked either side of this output secret.
// Both sides matter: an output that stops being sensitive is still one whose
// old value must not be printed.
func (o outputChange) sensitive() bool {
	return string(o.BeforeSensitive) == "true" || string(o.AfterSensitive) == "true"
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

	type row struct{ address, verb, detail string }
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
		// What is changing, not merely that something is. A row saying
		// "change proxmox_virtual_environment_vm.talos_cp[0]" and nothing else
		// is a tease: it tells a reviewer a machine is being altered and makes
		// them merge to find out how.
		var d string
		switch verb {
		case "change":
			d = detail("", changedAttributes(c.Change.Before, c.Change.After, c.Change.AfterUnknown))
		case "replace":
			d = detail("forced by ", replacedBecause(c.Change.ReplacePaths))
		}
		rows = append(rows, row{redactKeys(c.Address), verb, d})
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
		d := ""
		if doc.OutputChanges[name].sensitive() {
			// Worth saying out loud rather than leaving to inference. A
			// sensitive output is one this summary will never show, so a
			// reader who cannot see a value should know it was withheld on
			// purpose rather than absent by accident.
			d = "  (a secret this estate hands out; value withheld)"
		}
		rows = append(rows, row{"output." + name, verb, d})
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
		fmt.Fprintf(&b, "  %-*s  %s%s\n", width, r.verb, r.address, r.detail)
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
	b.WriteString("  (machines and other infrastructure)\n")

	// Everything that is not a work, in words rather than as a count.
	//
	// "1 output(s) to change" was a tease: it reported that something was
	// happening and left the reader to work out whether it mattered, which is
	// the opposite of what this comment is for. A count is only useful for
	// things a reader is already counting - machines - and an output is not
	// one of those.
	if counts["output"] > 0 {
		what := "a value this estate publishes"
		if counts["output"] > 1 {
			what = "values this estate publishes"
		}
		fmt.Fprintf(&b, "  %s %s rebuilt: %s will differ after this merge.\n",
			plural(counts["output"], "output", "outputs"), was(counts["output"]), what)
	}
	if counts["survey"] > 0 {
		fmt.Fprintf(&b, "  %s re-read from the live estate before anything is decided; nothing is changed by looking.\n",
			plural(counts["survey"], "data source is", "data sources are"))
	}

	// The one line a reviewer must not skim past. A converge that destroys is
	// almost always either deliberate and understood, or a mistake nobody
	// spotted - and the two look identical in a count.
	if counts["destroy"] > 0 || counts["replace"] > 0 {
		b.WriteString("\n  THIS PLAN DESTROYS OR REPLACES RESOURCES. Read every line above before merging.\n")
	}
	return b.String(), nil
}

// changedAttributes names the top-level attributes whose value differs, so a
// row says what is changing rather than only that something is.
//
// WHY NAMES ARE SAFE AND VALUES ARE NOT. A top-level key in a plan's before or
// after object is a provider schema attribute - `memory`, `cpu`, `disk`. Those
// are public API surface, identical in every estate that uses the provider,
// and they carry nothing about THIS estate. The values under them are the
// hostnames, addresses and credentials this repository keeps out of git, and
// the comment this feeds is world-readable.
//
// SO THE DEPTH LIMIT IS THE SAFETY PROPERTY, not a simplification. One level
// down, map-typed attributes have operator-supplied keys - a label, an
// annotation, a tag - and those can be a real hostname. `metadata.annotations`
// is safe to print; the key inside it is exactly the leak redactKeys exists to
// stop in addresses. So this never descends, and must not be "improved" to.
//
// after_unknown marks an attribute whose value is not computable until apply.
// It counts as changing: unknown-at-plan is how a change that depends on
// something being created shows up, and dropping it would hide precisely the
// attributes a converge is about to decide.
func changedAttributes(before, after, afterUnknown map[string]json.RawMessage) []string {
	seen := map[string]bool{}
	for name, unknown := range afterUnknown {
		// after_unknown carries `false` for known attributes as well as `true`
		// for unknown ones, so its mere presence proves nothing.
		if string(unknown) != "false" && string(unknown) != "null" {
			seen[name] = true
		}
	}
	for name, a := range after {
		b, had := before[name]
		if !had || !bytes.Equal(canonical(b), canonical(a)) {
			seen[name] = true
		}
	}
	for name := range before {
		if _, still := after[name]; !still {
			seen[name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// canonical re-encodes a value so two equal values compare equal regardless of
// key order or whitespace in the plan document. Comparing raw bytes without
// this reports an attribute as changed because its JSON was formatted
// differently, which is noise dressed as information.
func canonical(raw json.RawMessage) []byte {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

// replacedBecause names the attributes that force a replacement.
//
// A replacement destroys and rebuilds a machine, and the question a reviewer
// actually has is never "is it being replaced" - the verb says that - but
// "what made that necessary". tofu answers it in replace_paths and nothing
// here was reading it.
//
// Same depth rule as changedAttributes, and for the same reason: a path is a
// list of steps and only its first is guaranteed to be a schema attribute.
func replacedBecause(paths [][]json.RawMessage) []string {
	seen := map[string]bool{}
	for _, path := range paths {
		if len(path) == 0 {
			continue
		}
		var head string
		if err := json.Unmarshal(path[0], &head); err != nil {
			continue // A numeric index rather than an attribute name.
		}
		if head != "" {
			seen[head] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// detail renders the attribute list that follows an address, bounded so one
// wide resource cannot bury every other row. The cap is on display only - the
// count tells the reader there is more rather than implying there is not.
func detail(prefix string, names []string) string {
	if len(names) == 0 {
		return ""
	}
	const most = 6
	if len(names) > most {
		return fmt.Sprintf("  (%s%s and %d more)", prefix, strings.Join(names[:most], ", "), len(names)-most)
	}
	return fmt.Sprintf("  (%s%s)", prefix, strings.Join(names, ", "))
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

// plural picks a word for a count. Written out rather than reached for from a
// library because the estate's Go programs carry no dependencies, and because
// "1 output(s)" is the shape this exists to stop.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func was(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}
