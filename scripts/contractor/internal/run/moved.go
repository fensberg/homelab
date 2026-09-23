package run

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// A `moved` block and a targeted apply cannot both be pending.
//
// OpenTofu resolves a rename while it builds the plan, and it refuses to build
// a plan that records only part of one. So if either endpoint of a pending move
// falls outside `-target`, it stops:
//
//	Error: Moved resource instances excluded by targeting
//
// Every apply in the converge before the Cluster phase is targeted, so a single
// `moved` block anywhere in the configuration halts the converge at its FIRST
// apply - the disk image - and nothing downstream runs. Not the VMs, not the
// machine configuration, not the untargeted apply at the end of Cluster that
// would have settled the move and created everything else.
//
// That is not hypothetical. Renaming one R2 bucket resource stopped every
// converge on main: the four buckets the repository had started describing were
// never created, so the nightly backup failed with NoSuchBucket and the estate
// went back to having no off-site state; and the Talos machine configuration
// stopped being applied, so a metrics listener the repository declared stayed
// shut and Alertmanager paged every four hours about it.
//
// WHY REFRESH-ONLY, AND WHY TARGETED. Verified against OpenTofu 1.12.6 rather
// than assumed, by reproducing the failure on a throwaway configuration and
// then fixing it:
//
//   - `apply -refresh-only` settles the move and reports "0 added, 0 changed,
//     0 destroyed". It cannot create or destroy anything, which is what makes
//     it safe to run before a human has looked at any plan.
//   - It must be targeted at the move's own endpoints. An untargeted refresh
//     reads every data source in the configuration, including
//     data.talos_cluster_health - the read that sat for ninety minutes on a
//     healthy cluster during a teardown and that `destroyArgs` uses
//     `-refresh=false` to avoid. Confining the walk to the two addresses being
//     renamed avoids it entirely.
//   - `-refresh-only` with `-refresh=false` is refused outright ("OpenTofu
//     would have nothing to do"), so that combination is not an option.
//
// Targeting an address that no longer exists in the configuration - the `from`
// side, which is the whole point of a rename - is accepted, and is required:
// OpenTofu names both endpoints when it refuses.
//
// This runs on every converge and costs a bounded refresh of the renamed
// resources. Once a move is recorded the block becomes a no-op, and when
// somebody eventually deletes it this stops doing anything at all.

// movedBlock matches one `moved { ... }`. These blocks never nest, so
// stopping at the first closing brace is correct rather than lucky.
var movedBlock = regexp.MustCompile(`(?s)\bmoved\s*\{(.*?)\}`)

// movedEndpoint pulls the address off a `from =` or `to =` line. Addresses are
// bare references, never quoted, so this deliberately does not accept a string.
var movedEndpoint = regexp.MustCompile(`(?m)^\s*(from|to)\s*=\s*([A-Za-z_][\w.\[\]"-]*)\s*$`)

// PendingMoves returns every address named by a `moved` block in the OpenTofu
// source at dir, sorted and deduplicated.
//
// It reads the source rather than asking OpenTofu, because the question is
// "what does the configuration say" and answering it with a plan would need the
// plan that is refusing to build.
//
// Deliberately not in internal/tfsource, which says in its own header that it
// is not an HCL parser and must not grow into one. This is a second small
// reader with one job, not an extension of that one.
func PendingMoves(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		if readErr != nil {
			return nil, fmt.Errorf("reading %s: %w", e.Name(), readErr)
		}
		for _, block := range movedBlock.FindAllStringSubmatch(string(body), -1) {
			for _, endpoint := range movedEndpoint.FindAllStringSubmatch(block[1], -1) {
				seen[endpoint[2]] = true
			}
		}
	}

	out := make([]string, 0, len(seen))
	for addr := range seen {
		out = append(out, addr)
	}
	sort.Strings(out)
	return out, nil
}

// SettleMoves records any pending rename, so the targeted applies that follow
// are not refused. It is a no-op when the configuration declares no move.
func SettleMoves(ctx *Context) error {
	addrs, err := PendingMoves(ctx.ClusterDir)
	if err != nil {
		return fmt.Errorf("looking for renamed resources: %w", err)
	}
	if len(addrs) == 0 {
		return nil
	}

	Info(fmt.Sprintf("settling %d renamed resource address(es)", len(addrs)))
	if err := TofuApplyArgs(ctx, "settling renamed resources", settleMovesArgs(addrs)...); err != nil {
		return fmt.Errorf(`%w

A `+"`moved`"+` block is pending and could not be recorded. Until it is, every
targeted apply in this converge is refused with "Moved resource instances
excluded by targeting" - which means nothing downstream runs at all`, err)
	}
	return nil
}

// settleMovesArgs builds the invocation. Split out from the call for the same
// reason destroyArgs is: the flags are the decision here, and every one of them
// is load-bearing in a way that is not obvious from reading it.
//
// -refresh-only so it cannot create or destroy anything; -target per endpoint so
// the refresh does not walk the whole configuration and read
// data.talos_cluster_health; -json so the output goes through the summary rather
// than printing attribute values into a public log.
func settleMovesArgs(addrs []string) []string {
	args := []string{"apply", "-refresh-only", "-auto-approve", "-input=false", "-json"}
	for _, addr := range addrs {
		args = append(args, "-target="+addr)
	}
	return args
}
