package asbuilt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"homelab/details/tofustate"
)

// PlanStep is one targeted apply of the converge, as a plan sees it.
type PlanStep struct {
	Label   string
	Targets []string
}

// StepsInputs is what planning the converge's steps needs.
type StepsInputs struct {
	// Dir is an initialised copy of the root holding a copy of state. The
	// copy is what makes this a plan: settling a move writes to it, and
	// nothing reaches the state it was copied from.
	Dir string
	// Env is the environment tofu runs in; nil means the caller's own.
	Env []string
	// Sequence is the converge's steps in order. The last has no targets,
	// because the converge's last apply is untargeted, so its plan is the
	// whole of what the converge would do.
	Sequence []PlanStep
	// Refresh reads the real estate. False plans against the record,
	// offline, where refreshing would reach nothing.
	Refresh bool
	Say     func(string)
}

// PlanSteps plans what a converge does, the way the converge does it (#497).
//
// A converge settles pending renames, then applies target by target. A plan
// that ran one untargeted plan instead passed a pull request whose rename
// then refused every targeted apply, and halted converges on main for a day:
// an untargeted plan of a pending rename succeeds where a targeted one fails.
// So this settles the same renames in its copy, then plans each step with the
// same targets in the same order, and a step that would be refused is refused
// here, before the merge.
//
// Returns the last step's plan in JSON, which is the whole of the change.
func PlanSteps(in StepsInputs, tofu Tofu) ([]byte, error) {
	say := in.Say
	if say == nil {
		say = func(string) {}
	}
	if len(in.Sequence) == 0 || len(in.Sequence[len(in.Sequence)-1].Targets) != 0 {
		return nil, errors.New("the converge's steps must end with its untargeted apply, or the plan would not show the whole of the change")
	}
	if err := settleMoves(in, tofu, say); err != nil {
		return nil, err
	}

	common := []string{"plan", "-input=false", "-lock=false", "-no-color", "-out=step.tfplan"}
	if !in.Refresh {
		common = append(common, "-refresh=false")
	}
	for i, step := range in.Sequence {
		say(fmt.Sprintf("planning step %d of %d: %s", i+1, len(in.Sequence), step.Label))
		args := append([]string{}, common...)
		for _, t := range step.Targets {
			args = append(args, "-target="+t)
		}
		if _, stderr, err := tofu(in.Dir, in.Env, args...); err != nil {
			return nil, fmt.Errorf("the converge's step %q would be refused (%v):\n%s", step.Label, err, ErrorSummary(stderr))
		}
	}
	plan, _, err := tofu(in.Dir, in.Env, "show", "-json", "step.tfplan")
	_ = os.Remove(filepath.Join(in.Dir, "step.tfplan"))
	if err != nil {
		return nil, fmt.Errorf("reading the plan back: %w", err)
	}
	return plan, nil
}

// settleMoves records each pending rename in the copy.
//
// Against real state it is the converge's own operation: a refresh-only apply
// targeted at the move's endpoints, into the copy. Against the record there is
// no estate to refresh from, so the rename is recorded with `state mv`, which
// leaves the state in the same place. A move whose old address is not in state
// - already settled, or never built - has nothing to record.
func settleMoves(in StepsInputs, tofu Tofu, say func(string)) error {
	if n, err := ForgetReads(in.Dir, in.Env, tofu, false); err != nil {
		return err
	} else if n > 0 {
		say(fmt.Sprintf("forgot %d cached data-source read(s), as a converge does first", n))
	}
	moves, err := tofustate.Moves(in.Dir)
	if err != nil {
		return err
	}
	if len(moves) == 0 {
		return nil
	}
	say(fmt.Sprintf("settling %d renamed resource(s), as a converge does first", len(moves)))
	if in.Refresh {
		args := []string{"apply", "-refresh-only", "-auto-approve", "-input=false", "-lock=false", "-no-color"}
		for _, m := range moves {
			args = append(args, "-target="+m.From, "-target="+m.To)
		}
		if _, stderr, err := tofu(in.Dir, in.Env, args...); err != nil {
			return fmt.Errorf("settling renamed resources, as a converge would, failed (%v):\n%s", err, ErrorSummary(stderr))
		}
		return nil
	}
	listed, _, err := tofu(in.Dir, in.Env, "state", "list")
	if err != nil {
		return fmt.Errorf("listing the record's state: %w", err)
	}
	addrs := strings.Fields(string(listed))
	for _, m := range moves {
		if !inState(addrs, m.From) || inState(addrs, m.To) {
			continue
		}
		if _, stderr, err := tofu(in.Dir, in.Env, "state", "mv", "-lock=false", m.From, m.To); err != nil {
			return fmt.Errorf("recording the rename of %s (%v):\n%s", m.From, err, ErrorSummary(stderr))
		}
	}
	return nil
}

// ForgetReads removes every data source's cached read from state, and says
// how many there were.
//
// A data source in state is only a cache: every plan reads it again. But a
// cached read at an address the configuration no longer uses - `x` after `x`
// gained a count, so the configuration says `x[0]` - is an implicit move, and a
// pending move refuses every targeted apply. That is how #555, giving the
// cluster health read a count, would have halted the next converge at its
// first step; the plan walking the converge's steps is what found it. So the
// converge and both plans forget the cached reads before their first targeted
// step, and there is nothing stale left to move.
//
// lock is false in a copy of state, where nothing else can hold it.
func ForgetReads(dir string, env []string, tofu Tofu, lock bool) (int, error) {
	listed, stderr, err := tofu(dir, env, "state", "list")
	if err != nil {
		return 0, fmt.Errorf("listing state to forget its cached reads (%v):\n%s", err, ErrorSummary(stderr))
	}
	var reads []string
	for _, a := range strings.Fields(string(listed)) {
		if strings.HasPrefix(a, "data.") || strings.Contains(a, ".data.") {
			reads = append(reads, a)
		}
	}
	if len(reads) == 0 {
		return 0, nil
	}
	args := []string{"state", "rm"}
	if !lock {
		args = append(args, "-lock=false")
	}
	if _, stderr, err := tofu(dir, env, append(args, reads...)...); err != nil {
		return 0, fmt.Errorf("forgetting cached data-source reads (%v):\n%s", err, ErrorSummary(stderr))
	}
	return len(reads), nil
}

// inState reports whether a resource, or any instance of it, is in state.
func inState(addrs []string, resource string) bool {
	for _, a := range addrs {
		if a == resource || strings.HasPrefix(a, resource+"[") {
			return true
		}
	}
	return false
}

// CopyRoot copies a root's configuration into dir, at the same depth, for
// a plan that must not touch the root's own state. See copyRoot.
func CopyRoot(from, to string) error { return copyRoot(from, to) }
