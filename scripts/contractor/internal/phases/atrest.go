package phases

import (
	"encoding/json"
	"fmt"
	"homelab/details/console"
	"regexp"
	"strings"
	"time"

	"homelab/contractor/internal/run"
	"homelab/details/flux"
)

// A converge does not start while its own state database is about to move.
//
// The estate's state is in a database the estate runs. A merge that changes
// that database's manifest does two things from one commit, seconds apart:
// Flux applies the change, which restarts every instance, and the deploy
// workflow starts a converge, which keeps its state in those instances. The
// converge was mid-apply when the database went down under it, and ended
// unable to say whether the estate had changed (#690, 2026-10-11). An earlier
// merge had done the same and missed by timing.
//
// Take-over's own wait cannot see this. It waits for the database's port, and
// a port answers while an instance is on its way down.
//
// So, once the state has been reached and before anything is applied, two
// things are asked of the cluster:
//
//   - Has Flux applied the commit this converge is of? Asked only where a
//     converge runs unattended, since that is where it races the merge that
//     started it. Until Flux has, a restart the commit asks for has not begun,
//     and a database that looks at rest is only one that has not been told.
//   - Is the state database at rest: every instance ready, and nothing being
//     rolled out? Asked twice, a pause apart, because the operator takes a
//     moment to act on a change and a single look can land in that moment.
//
// It cannot cover a restart that begins after a converge has started, which
// nothing in the estate asks for on its own.

// restPause is how long apart the two looks at the database are.
var restPause = 20 * time.Second

// nothingApplied is what a converge says when it gives up waiting here.
const nothingApplied = `This converge has applied nothing: it stopped before its first change, so
the estate is as it was. Run it again once the cluster has settled.`

// atRestHealthy is what the operator calls a cluster it is doing nothing to.
const atRestHealthy = "Cluster in healthy state"

func awaitRest(ctx *run.Context) error {
	kubeconfig, cleanup, err := writeKubeconfig(ctx.In(ctx.Cluster))
	if err != nil {
		return err
	}
	defer cleanup()

	if console.InActions() {
		head, err := run.CmdOutputQuiet(".", "git", "rev-parse", "HEAD")
		if err != nil {
			return fmt.Errorf("could not read this checkout's commit, so Flux cannot be shown to have applied it: %w", err)
		}
		requestFluxReconcile(ctx, kubeconfig, time.Now())
		applied := func(ctx *run.Context, kubeconfig string) error {
			out, err := kubectl(ctx, kubeconfig, "get", flux.Kustomizations, "-A", "-o", "json")
			if err != nil {
				return err
			}
			return fluxBehind(out, strings.TrimSpace(head))
		}
		if err := waitUntil(ctx, kubeconfig, "Flux to have applied this commit", 10*time.Minute, applied, nothingApplied); err != nil {
			return &NotAtRestError{Err: err}
		}
	}
	rest := func(ctx *run.Context, kubeconfig string) error {
		for look := 0; look < 2; look++ {
			if look > 0 {
				time.Sleep(restPause)
			}
			out, err := kubectl(ctx, kubeconfig, "get", "clusters.postgresql.cnpg.io", "-A", "-o", "json")
			if err != nil {
				return err
			}
			if err := databaseMoving(out); err != nil {
				return err
			}
		}
		return nil
	}
	if err := waitUntil(ctx, kubeconfig, "the state database to be at rest", 15*time.Minute, rest, nothingApplied); err != nil {
		return &NotAtRestError{Err: err}
	}
	return nil
}

// NotAtRestError is a converge that gave up waiting for the cluster to be at
// rest, having applied nothing.
//
// It is a failure of its own kind because of what it does not say. A
// converge that stops before its first change usually stopped at something
// the change did, and returning the repository to the commit before is then
// exact. This one stopped at the cluster: Flux had not applied the commit,
// or the state database was being rolled. Nothing under management/ is
// known to be at fault and returning it cannot bring a cluster to rest. On
// 2026-10-12 a revert was opened for one of these, of two files the
// converge does not apply, and told the operator to merge it (#713).
type NotAtRestError struct{ Err error }

func (e *NotAtRestError) Error() string { return e.Err.Error() }
func (e *NotAtRestError) Unwrap() error { return e.Err }

// fluxBehind says which of the Kustomizations that follow the repository have
// not applied a commit, and is nil when all have.
//
// One that follows something else - a release of an application, by its
// digest - says nothing of this commit either way and is left out. None
// following the repository at all is an error: it would otherwise read as
// every one of them being up to date.
func fluxBehind(body []byte, commit string) error {
	var list struct {
		Items []struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				LastAppliedRevision string `json:"lastAppliedRevision"`
				Conditions          []struct {
					Type    string `json:"type"`
					Reason  string `json:"reason"`
					Message string `json:"message"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return fmt.Errorf("parsing kubectl output: %w", err)
	}
	const ofACommit = "@sha1:"
	following := 0
	var behind []string
	for _, k := range list.Items {
		if k.Kind != flux.Kustomization {
			continue
		}
		at := k.Status.LastAppliedRevision
		cut := strings.Index(at, ofACommit)
		if cut < 0 {
			continue
		}
		following++
		if at[cut+len(ofACommit):] != commit {
			line := fmt.Sprintf("%s is at %s", k.Metadata.Name, short(at[cut+len(ofACommit):]))
			// And why, where Flux says: what it was refused, said as the
			// object, the field and the rule (flux.Refusal), since this is
			// read in a public log; or which layer it waits behind. Without
			// it the converge said only that it waited, and finding why
			// took a person and kubectl (#766).
			for _, c := range k.Status.Conditions {
				if c.Type != "Ready" || c.Message == "" {
					continue
				}
				if m := waitsOn.FindStringSubmatch(c.Message); m != nil {
					line += ", waiting on " + m[1]
				} else if c.Reason != "Progressing" {
					line += ", refused: " + flux.Refusal(c.Message)
				}
			}
			behind = append(behind, line)
		}
	}
	if following == 0 {
		return fmt.Errorf("no Kustomization says which commit of the repository it has applied, so none can be shown to have applied this one")
	}
	if len(behind) > 0 {
		return fmt.Errorf("%d Kustomization(s) have not applied %s yet:\n  %s", len(behind), short(commit), strings.Join(behind, "\n  "))
	}
	return nil
}

// databaseMoving says what the state database is in the middle of, and is
// nil when it is at rest.
func databaseMoving(body []byte) error {
	ready, want, err := databaseInstances(body)
	if err != nil {
		return err
	}
	var list struct {
		Items []struct {
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return fmt.Errorf("parsing kubectl output: %w", err)
	}
	for _, c := range list.Items {
		if c.Status.Phase != atRestHealthy {
			return fmt.Errorf("the state database is not at rest: its operator says %q, with %d of %d instances ready", c.Status.Phase, ready, want)
		}
	}
	if ready != want {
		return fmt.Errorf("the state database is not at rest: %d of %d instances are ready", ready, want)
	}
	return nil
}

// waitsOn is the layer a Kustomization says it waits behind.
var waitsOn = regexp.MustCompile(`dependency '[a-z0-9-]+/([a-z0-9-]+)' is not ready`)
