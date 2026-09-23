package run

import (
	"fmt"
	"regexp"
	"strings"
)

// AdoptIfOrphaned imports a resource that already exists outside Terraform -
// a prior run's incomplete teardown left it behind - before apply gets a
// chance to fail trying to create a duplicate.
//
// This exists in Go rather than as a `.tf` import block on purpose: import
// blocks always attempt the read and hard-fail when the target genuinely
// does not exist ("failed reading ..."), which is the normal, common case
// for these resources. There is no declarative way to make an import
// conditional on the object actually being there first - confirmed the hard
// way, once, when an import block broke a completely ordinary fresh-create
// run the same day it was added.
//
// No-ops if the resource is already tracked, or if findID reports there is
// genuinely nothing there yet (an empty id, no error).
func AdoptIfOrphaned(ctx *Context, address string, findID func() (id string, err error)) error {
	if InState(ctx, address) {
		return nil
	}

	importID, err := findID()
	if err != nil {
		return fmt.Errorf("checking whether %s already exists outside Terraform: %w", address, err)
	}
	if importID == "" {
		return nil
	}

	Info(fmt.Sprintf("%s already exists outside Terraform - importing it instead of letting apply try to create a duplicate", address))
	return Tofu(ctx, "tofu import "+address, "import", "-input=false", address, importID)
}

// InState reports whether Terraform is already tracking an address.
//
// stderr is deliberately discarded. `tofu state list <address>` writes a full
// "Error: Unknown resource instance" block when the address is not tracked,
// and not being tracked is the ordinary, expected answer here - so printing it
// made every healthy run look as though it had just failed.
func InState(ctx *Context, address string) bool {
	out, err := CmdOutputQuiet(ctx.ClusterDir, "tofu", "state", "list", address)
	return err == nil && strings.TrimSpace(out) != ""
}

// TrackedResourceName reads one attribute off a resource already in state.
//
// `tofu state show` rather than `show -json`: the JSON form serialises the
// whole state, which for this estate means every VM, every machine secret and
// every credential, in order to read one string. A targeted show reads one
// resource, and nothing here needs more than that.
//
// Returns "" when the address is not tracked or the attribute is absent, which
// callers must treat as "cannot tell" rather than as "no name".
func TrackedResourceName(ctx *Context, address string) string {
	out, err := CmdOutputQuiet(ctx.ClusterDir, "tofu", "state", "show", "-no-color", address)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		m := trackedName.FindStringSubmatch(line)
		if m != nil {
			return m[1]
		}
	}
	return ""
}

// The `name = "..."` line of a `tofu state show`, anchored so an attribute
// merely ending in "name" - display_name, bucket_name - cannot match.
var trackedName = regexp.MustCompile(`^\s*name\s+=\s+"([^"]*)"\s*$`)

// ReleaseIfRenamed stops tracking a resource whose real name no longer matches
// the one the config asks for, so the next adopt can pick up the right one.
//
// WHY THIS EXISTS, AND WHY IT IS NOT A DESTROY.
//
// An object storage bucket's name cannot be changed in place - the provider
// forces replacement - and the vendor refuses to delete a bucket that holds
// objects. So a rename plans as destroy-and-create and then fails on the
// destroy, leaving the estate unable to converge at all. The failure is not
// recoverable by the operator either: every phase runs inside one process that
// sterilizes the backend configuration on exit, so there is no supported way
// to reach the state by hand, deliberately.
//
// Releasing rather than destroying is the same choice the teardown already
// makes for the buckets that outlive the estate. The old bucket keeps every
// object in it and simply stops being this estate's business; retiring it is a
// deliberate act by somebody who has checked what is inside, not a side effect
// of a rename.
//
// The window where nothing tracks it is real and is stated out loud rather
// than hidden, because an untracked bucket is exactly the kind of thing this
// estate calls a landmine.
func ReleaseIfRenamed(ctx *Context, address, want string) error {
	if !InState(ctx, address) {
		return nil
	}

	have := TrackedResourceName(ctx, address)
	if have == "" || have == want {
		// Unreadable is not the same as different, and guessing in this
		// direction would release a resource on a parse failure.
		return nil
	}

	Warn(fmt.Sprintf("%s tracks a resource named %q, and the config now asks for %q", address, have, want))
	Warn("Releasing the old one rather than destroying it: it keeps whatever it holds, and nothing here will touch it again.")
	Warn("It is now tracked by nothing. Retire it deliberately once you have checked what is in it.")

	if _, err := CmdOutputQuiet(ctx.ClusterDir, "tofu", "state", "rm", address); err != nil {
		return fmt.Errorf("releasing %s, which holds %q and cannot be renamed in place: %w", address, have, err)
	}
	Ok(fmt.Sprintf("released %s; the adopt will pick up %q", address, want))
	return nil
}
