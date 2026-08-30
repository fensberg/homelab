package phases

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"homelab/steward/internal/config"
	"homelab/steward/internal/run"
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
// check-vault already draws between "the reference resolves" and "here is what
// it resolved to", for the same reason: the output is most useful exactly when
// somebody wants to paste it somewhere.
func Plan(ctx *run.Context) error {
	run.WritePhase("Plan", "Show what a converge would change, without changing it.")

	// The saved plan file holds the values this summary refuses to print, so
	// it never outlives the phase that made it. Sterilize lists it too, for
	// the run that dies before reaching this line.
	defer func() { _ = os.Remove(ctx.TofuPlanFile) }()

	// Refuse fast when the estate is mid-scale, rather than waiting ten
	// minutes to fail.
	//
	// data.talos_cluster_health is configured with every node IP the config
	// declares, and reads at plan time. Raise control_plane_count and it waits
	// for nodes that do not exist yet, times out, and produces no plan at all.
	// An apply does not have that problem - the graph creates the VMs before
	// the health read - so the answer is a converge rather than a longer wait.
	if err := assertNodeCountMatchesState(ctx); err != nil {
		return err
	}

	run.Info("planning")
	if err := run.Tofu(ctx, "tofu plan",
		"plan", "-input=false", "-out="+ctx.TofuPlanFile,
	); err != nil {
		return err
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
	return nil
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
		rows = append(rows, row{c.Address, verb})
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

// assertNodeCountMatchesState compares the control plane the config asks for
// against the one the state describes.
func assertNodeCountMatchesState(ctx *run.Context) error {
	cfg, err := config.LoadRendered(ctx.ConfigRendered)
	if err != nil {
		return err
	}
	net, err := config.ResolveSiteNetwork(cfg, ctx.Site)
	if err != nil {
		return err
	}

	out, err := run.CmdOutput(ctx.ClusterDir, "tofu", "state", "list")
	if err != nil {
		return fmt.Errorf("listing state to compare the node count: %w", err)
	}

	want := len(net.VMNames)
	got := countControlPlaneVMs(out)
	if got == want {
		return nil
	}
	return fmt.Errorf(`this change alters the control plane's size, and a plan cannot show it.

The config asks for %d control-plane node(s); the state describes %d. The
cluster-health data source reads at plan time against every node the config
declares, so it would wait for nodes that do not exist yet and time out
without producing anything.

An apply does not have that problem - it creates the machines before reading
their health - so the way to see this change is to make it:

    task converge SITE=%s

That is not a workaround for a missing feature; it is the one case where a
plan genuinely cannot answer the question`, want, got, ctx.Site)
}

// countControlPlaneVMs counts control-plane VM instances in `tofu state list`
// output, ignoring the template VM and every other resource.
func countControlPlaneVMs(stateList string) int {
	n := 0
	for _, line := range strings.Split(stateList, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "proxmox_virtual_environment_vm.talos_cp[") {
			n++
		}
	}
	return n
}
