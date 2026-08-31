package onepassword

import (
	"os/exec"
	"strings"
)

// Status is what a probe of one op:// reference found. It is deliberately the
// only thing a probe returns.
//
// `op` has no existence check that does not also return the value - the same
// limitation secrets.go already names where it probes for the break-glass
// identity. So Probe below does read the value, and the honest way to build on
// that is to make the value structurally impossible to get back out: it is
// read into a local, measured, and dropped when the function returns. A caller
// cannot print what it was never handed, which is a stronger guarantee than a
// caller that is merely trusted not to.
type Status int

const (
	// StatusOK: the reference resolves and the field has content.
	StatusOK Status = iota
	// StatusEmpty: the field exists but is blank. `op inject` treats this as
	// success and writes an empty string, which then reaches a provider as
	// "credentials are empty" with nothing naming the field at fault.
	StatusEmpty
	// StatusMissing: the item or field does not exist, or the path is
	// misspelled. This is what a dangling reference in the template looks
	// like, and it fails the run at the Render phase.
	StatusMissing
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusEmpty:
		return "empty"
	default:
		return "missing"
	}
}

// Probe resolves one reference and reports only whether it resolved and
// whether it had content. The value itself never leaves this function.
func Probe(ref string) Status {
	out, err := exec.Command("op", "read", ref).Output()
	if err != nil {
		return StatusMissing
	}
	if strings.TrimSpace(string(out)) == "" {
		return StatusEmpty
	}
	return StatusOK
}
