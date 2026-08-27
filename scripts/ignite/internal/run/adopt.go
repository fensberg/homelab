package run

import (
	"fmt"
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
