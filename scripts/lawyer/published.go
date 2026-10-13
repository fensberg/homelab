package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"homelab/details/console"
)

// withheld says why a failure in a public log comes without tofu's own words.
const withheld = ". What tofu printed is withheld: this log is public, and tofu quotes the values it was working with. Run the same verb on a workstation to read it"

// tofuUnseen runs tofu and shows nothing it prints.
//
// An apply of the estate prints every attribute of everything it touches -
// a site's name, an address, the estate's domain - and a run started by a
// merge prints into the log of a public repository. So there the estate is
// applied unseen, and what is said is what the lawyer says itself: each
// thing by its address and what is done to it, as a plan is said.
func (e *estate) tofuUnseen(args ...string) error {
	c := exec.Command("tofu", args...)
	c.Dir = e.dir
	c.Env = e.backendEnv()
	c.Stdout = io.Discard
	c.Stderr = io.Discard
	if err := c.Run(); err != nil {
		return fmt.Errorf("tofu %s: %w%s", args[0], err, withheld)
	}
	return nil
}

// apply brings the estate to what is declared.
//
// On a workstation it is tofu's own apply, seen whole by the one person at
// the terminal. Where the log is public it plans first, says what the plan
// would change by address and action, and then applies that plan and no
// other: what was said is what is done.
func (e *estate) apply() error {
	if !e.published {
		return e.run("apply", "-input=false", "-auto-approve")
	}
	saved := filepath.Join(e.dir, "estate.tfplan")
	defer os.Remove(saved)
	shown, err := e.planTo(saved)
	if err != nil {
		return err
	}
	changes, err := plannedChanges([]byte(shown))
	if err != nil {
		return err
	}
	for _, line := range changes {
		console.Info(line)
	}
	if len(changes) == 0 {
		console.Info("nothing to change: the estate is as management/estate/ declares it")
	}
	return e.run("apply", "-input=false", saved)
}
