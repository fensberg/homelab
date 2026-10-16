package phases

import (
	"encoding/base64"
	"errors"
	"fmt"

	"homelab/contractor/internal/run"
	"homelab/details/onepassword"
)

// The Talos client configuration is kept in the site's vault, for what rolls
// the machines.
//
// The roll is an Ansible play (management/roll), run as `op run --
// ansible-playbook`, and that is how a standard play is given a credential:
// from the vault, for as long as the run. Until now the configuration was
// written out of OpenTofu's state for each use, which only the contractor
// can do (the state is encrypted, and in the cluster's own database). So the
// converge, which has it, keeps it where the play can read it.
//
// It is no more exposed there than it was. Anything that can read the site's
// vault can already read the state's key and the state, and the
// configuration with it.
//
// Written only when it differs from what the vault holds, so a converge that
// changed nothing writes nothing (onepassword.WriteItem).

// TalosAccessItem and TalosAccessField are where a site's vault keeps the
// configuration: op://<site>/talos/talosconfig, base64 on one line.
const (
	TalosAccessItem  = "talos"
	TalosAccessField = "talosconfig"
)

// keepInVault is how an item is written: onepassword.WriteItem, and a
// stand-in in the tests that run a whole verb.
var keepInVault = onepassword.WriteItem

// keepTalosAccess reads the Talos client configuration out of the cluster
// root, which the converge has initialised and just applied, and keeps it in
// the site's vault. It is read straight from the root rather than through
// writeTalosconfigTo, which renders and takes over first: that is right for a
// command run between converges and wrong in the middle of one.
func keepTalosAccess(ctx *run.Context, read func() (string, error), write func(vault, title string, fields map[string]string) ([]string, error)) error {
	raw, err := read()
	if err == nil && !looksLikeTalosconfig(raw) {
		err = errors.New("what the cluster root holds is not a Talos client configuration")
	}
	if err == nil {
		err = namesNodes(raw)
	}
	if err != nil {
		return fmt.Errorf("the Talos client configuration could not be read to keep in the vault: %w", err)
	}
	changed, err := write(ctx.Site, TalosAccessItem, map[string]string{TalosAccessField: base64.StdEncoding.EncodeToString([]byte(raw))})
	if err != nil {
		return fmt.Errorf("the Talos client configuration could not be kept in the vault: %w", err)
	}
	if len(changed) > 0 {
		run.Ok("kept the Talos client configuration in the site's vault, for the roll")
	}
	return nil
}
