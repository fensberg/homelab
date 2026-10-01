package phases

import (
	"fmt"
	"homelab/details/tcp"
	"os"
	"strings"
	"time"

	"homelab/contractor/internal/run"
)

// THE FILENAME CARRIES THE HYPHEN ON PURPOSE, and it is not idiomatic Go.
//
// converge_order_test.go finds a phase's code by its own name - take-over.go
// for "take-over" - and that is what makes the ordering rule enforceable: a
// phase whose file it cannot find is a phase nothing checks, silently. Naming
// this takeover.go compiled, passed review and turned the guard off for this
// one phase, which the pre-push hook caught.
//
// So the file matches the phase rather than the convention. Moving the work to
// where the guard already looks is this repository's rule; widening the guard
// to accept a second spelling would have bought a filename and paid for it with
// an exception in the thing doing the checking.

// TakeOver reconnects this workspace to the state of an estate that already
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
// TakeOver is deliberately not part of AllPhases. Ignition creates the cluster
// that holds the state; it cannot begin by connecting to it.
func TakeOver(ctx *run.Context) error {
	run.WritePhase("Take-over", "Reconnect to the state of an estate that already exists.")

	for _, root := range ctx.Roots() {
		if _, err := os.Stat(root.LocalState); err == nil {
			return fmt.Errorf(`local state already exists at %s, so this workspace is mid-ignition rather than detached.

Converge is for an estate whose state already lives in the cluster. If an
earlier run stopped before Migrate, finish it with -from migrate instead; if it
left state behind after a failure, that state is the authoritative copy and
deleting it would strand whatever it describes`, root.LocalState)
		}
	}

	for _, root := range ctx.Roots() {
		if _, err := os.Stat(root.BackendPgOn); err != nil {
			if err := copyFile(root.BackendPgOff, root.BackendPgOn); err != nil {
				return fmt.Errorf("enabling the Postgres backend for the %s root: %w", root.Name, err)
			}
		}
	}

	connStr, host, port, err := buildStateConnStr(ctx)
	if err != nil {
		return err
	}

	run.Info(fmt.Sprintf("waiting for the state database at %s:%d ...", host, port))
	if !tcp.Await(tcp.Addr(host, port), 5*time.Minute, 15*time.Second) {
		return fmt.Errorf(`the state database at %s:%d never answered.

Converge applies a change to a running estate, so the cluster holding its state
has to be up. If the cluster is gone, this is a restore rather than a converge:
see 'contractor restore'`, host, port)
	}

	// Both roots, and both must hold something. A site whose cluster root
	// has state and whose platform root has none is not an estate to
	// converge: a plan would create every namespace and secret a second
	// time, beside the ones already running.
	total := 0
	for _, root := range ctx.Roots() {
		in := ctx.In(root)
		if err := run.Tofu(in, "tofu init (pg backend)",
			"init", "-input=false", "-reconfigure",
			"-backend-config=conn_str="+connStr,
		); err != nil {
			return fmt.Errorf("could not take over the %s root's state: %w", root.Name, err)
		}

		out, err := run.CmdOutput(in.Dir, "tofu", "state", "list")
		if err != nil {
			return fmt.Errorf("attached to the backend but could not list the %s root's state: %w", root.Name, err)
		}
		n := len(strings.Fields(out))
		if n == 0 {
			return fmt.Errorf(`attached to the state database, and the %s root's state is empty.

That is not an estate to converge - it is an empty backend that a plan would
fill by building a second copy of everything beside whatever is already
running. Nothing here can tell which of those two situations you are in, so it
stops.

If this really is a new estate, run ignition rather than converge. If it is not,
the state has been lost and belongs in 'contractor restore'`, root.Name)
		}
		total += n
	}

	// Record what the state looked like before this run could change it.
	//
	// If this run later fails, comparing serials answers whether anything was
	// actually written - which is the difference between a failure that can be
	// reverted automatically and one where reverting would make the config
	// wrong in the other direction. Recorded here rather than in each applying
	// phase because this is the last point at which nothing can have changed
	// yet.
	if serial, ok := estateSerial(ctx); ok {
		ctx.StateSerialAtTakeover = serial
		ctx.TakenOverOK = true
	}

	run.Ok(fmt.Sprintf("took over existing state: %d resource(s)", total))
	return nil
}

// EstateChanged reports whether this run wrote anything to the estate's state.
//
// Three answers, and collapsing any two of them is the bug this exists to
// prevent. The banner it feeds used to assert "the estate is untouched by this
// failure" on every converge failure with no condition at all - true when the
// run died at TakeOver, and a confident falsehood when it died halfway through
// creating machines, which is precisely when somebody most needs to look.
//
//	changed=false, certain=true   nothing was written; a revert is exact
//	changed=true,  certain=true   something was written; a revert is a guess
//	certain=false                 the state could not be read; say so
func EstateChanged(ctx *run.Context) (changed, certain bool) {
	// Never attached, so nothing in this run could have reached tofu at all.
	// tests/go/repo/converge_order_test.go is what makes this sound: no phase
	// before take-over may invoke tofu, so there is no path to a write.
	if !ctx.TakenOverOK {
		return false, true
	}
	serial, ok := estateSerial(ctx)
	if !ok {
		return false, false
	}
	return serial != ctx.StateSerialAtTakeover, true
}

// estateSerial is the serials of both roots' state, added. Each only ever
// rises, so the sum moves exactly when either root was written, which is the
// question. Not ok unless both could be read.
func estateSerial(ctx *run.Context) (serial int64, ok bool) {
	for _, root := range ctx.Roots() {
		n, ok := run.StateSerial(ctx.In(root))
		if !ok {
			return 0, false
		}
		serial += n
	}
	return serial, true
}
