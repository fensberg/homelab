package main

import (
	"fmt"
	"sort"
	"strings"

	"homelab/details/console"
	"homelab/details/tofustate"
)

// plan shows what a converge of the estate would change and changes
// nothing.
//
// A converge of the estate applies at once, with nobody shown what it is
// about to do. That was borne while the estate was a tunnel and a list of
// members. It now sets who may send work to the estate and what that work
// may do, where a declaration written wrongly locks everybody out of the
// repository or stops every workflow, and it is to run on a timer with
// nobody watching. So what it would do can be asked first.
//
// Each thing by its address and what would be done to it, and never a
// value: a plan holds every attribute of everything it touches, and what
// this prints is what somebody pastes into a pull request. The same line a
// site's plan draws, for the same reason.
func (e *estate) plan() error {
	shown, err := e.shownPlan("estate.tfplan")
	if err != nil {
		return err
	}
	changes, err := plannedChanges([]byte(shown))
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		console.Ok("a converge would change nothing: the estate is as management/estate/ declares it")
		return nil
	}
	for _, line := range changes {
		console.Info(line)
	}
	console.Warn(fmt.Sprintf("a converge would change %d thing(s). Nothing was changed", len(changes)))
	return nil
}

// plannedChanges is every resource a plan would change, as its address and
// what would be done to it, sorted. One the plan leaves as it is, or only
// reads, is not one.
func plannedChanges(showJSON []byte) ([]string, error) {
	p, err := tofustate.ParsePlan(showJSON)
	if err != nil {
		return nil, fmt.Errorf("reading the plan: %w", err)
	}
	var out []string
	for _, rc := range p.ResourceChanges {
		if rc.Mode != "managed" {
			continue
		}
		done := strings.Join(rc.Change.Actions, " then ")
		if done == "" || done == "no-op" || done == "read" {
			continue
		}
		out = append(out, rc.Address+": "+done)
	}
	sort.Strings(out)
	return out, nil
}
