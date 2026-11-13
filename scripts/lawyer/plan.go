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
// what would be done to it - for an update, with the names of the attributes
// it alters - sorted. One the plan leaves as it is, or only
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
		// An update is said with what it alters, by name: an attribute that
		// changes on every converge is a difference that never settles, and
		// the name is what finds it (#885). Never a value.
		if done == "update" {
			if names := rc.ChangedAttributes(); len(names) > 0 {
				done += " (" + strings.Join(names, ", ") + ")"
			}
		}
		out = append(out, rc.Address+": "+done)
	}
	sort.Strings(out)
	return out, nil
}

// readBack plans the estate again straight after a converge, and fails when
// that plan would change anything.
//
// A converge that leaves something to change has not brought the estate to
// what management/estate/ declares, whatever its apply reported: the next
// converge changes it again, and the one after. The default device profile
// did that for six converges before anyone read the log (#885). Each change
// is said as a plan says it - by address, and for an update by the names of
// the attributes it alters - so the failure names what does not settle.
// There is no second apply: what does not settle is fixed in its
// declaration, not by applying until it seems to.
func (e *estate) readBack() error {
	shown, err := e.shownPlan("readback.tfplan")
	if err != nil {
		return err
	}
	changes, err := plannedChanges([]byte(shown))
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		console.Ok("the estate is as management/estate/ declares it: a plan straight after the converge changes nothing")
		return nil
	}
	for _, line := range changes {
		console.Info(line)
	}
	return fmt.Errorf("the estate did not settle: straight after the converge, a plan would still change %d thing(s), listed above. Each is changed again by every converge, so its declaration is not what the vendor keeps; fix the declaration", len(changes))
}
