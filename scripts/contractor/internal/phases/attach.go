package phases

import (
	"fmt"
	"os"
	"strings"
	"time"

	"homelab/contractor/internal/run"
)

// Attach reconnects this workspace to the state of an estate that already
// exists, so a change can be applied to it rather than a second copy of it
// being built beside it.
//
// It exists because the ignition path could not do this. Migrate moves state
// local -> Postgres and Sterilize then deletes both the local state file and
// backend_pg.tf, which is correct: a workstation should hold nothing after a
// run. But it means the next run starts with an empty workspace, plans to
// create every VM from scratch, and - if it reached Migrate - would copy that
// empty state over the real one with -force-copy. The button was a first-run
// tool and nothing said so.
//
// Attach is deliberately not part of AllPhases. Ignition creates the cluster
// that holds the state; it cannot begin by connecting to it.
func Attach(ctx *run.Context) error {
	run.WritePhase("Attach", "Reconnect to the state of an estate that already exists.")

	// Local state means an ignition run that never reached Migrate, or one
	// that was interrupted. Either way the workspace already holds the
	// authoritative copy, and attaching to Postgres on top of it would leave
	// two states describing one estate with nothing to say which is right.
	if _, err := os.Stat(ctx.LocalState); err == nil {
		return fmt.Errorf(`local state already exists at %s, so this workspace is mid-ignition rather than detached.

Converge is for an estate whose state already lives in the cluster. If an
earlier run stopped before Migrate, finish it with -from migrate instead; if it
left state behind after a failure, that state is the authoritative copy and
deleting it would strand whatever it describes`, ctx.LocalState)
	}

	if _, err := os.Stat(ctx.BackendPgOn); err != nil {
		if err := copyFile(ctx.BackendPgOff, ctx.BackendPgOn); err != nil {
			return fmt.Errorf("enabling the Postgres backend: %w", err)
		}
	}

	connStr, host, port, err := buildStateConnStr(ctx)
	if err != nil {
		return err
	}

	run.Info(fmt.Sprintf("waiting for the state database at %s:%d ...", host, port))
	if !run.WaitForPort(host, port, 5*time.Minute, 15*time.Second) {
		return fmt.Errorf(`the state database at %s:%d never answered.

Converge applies a change to a running estate, so the cluster holding its state
has to be up. If the cluster is gone, this is a restore rather than a converge:
see 'contractor restore'`, host, port)
	}

	// -reconfigure, never -migrate-state. Migration is the verb that copies
	// one state over another, and this workspace has nothing worth copying:
	// pointing it at the backend is the whole intent.
	if err := run.Tofu(ctx, "tofu init (pg backend)",
		"init", "-input=false", "-reconfigure",
		"-backend-config=conn_str="+connStr,
	); err != nil {
		return fmt.Errorf("could not attach to the state database: %w", err)
	}

	// The check this phase exists for.
	//
	// An init against an empty backend succeeds exactly as loudly as one
	// against a populated backend. Applying after that would create a second
	// estate beside the first - same names, same VM ids, same addresses - and
	// the first sign of it would be Proxmox refusing a duplicate, or worse,
	// not refusing. So prove the state describes something before letting any
	// later phase act on it.
	out, err := run.CmdOutput(ctx.ClusterDir, "tofu", "state", "list")
	if err != nil {
		return fmt.Errorf("attached to the backend but could not list its state: %w", err)
	}
	n := len(strings.Fields(out))
	if n == 0 {
		return fmt.Errorf(`attached to the state database, and it is empty.

That is not an estate to converge - it is an empty backend that a plan would
fill by building a second copy of everything beside whatever is already
running. Nothing here can tell which of those two situations you are in, so it
stops.

If this really is a new estate, run ignition rather than converge. If it is not,
the state has been lost and belongs in 'contractor restore'`)
	}

	run.Ok(fmt.Sprintf("attached to existing state: %d resource(s)", n))
	return nil
}
