package phases

import "fmt"

// Unavailable means the question could not be asked, not that the answer was no.
//
// The Health phase once spent five minutes retrying `talosctl etcd members`
// against a runner where talosctl was not installed, and then reported "etcd
// membership did not become healthy within 5m0s". The cluster's etcd was never
// measured. The banner said it was measured and found wanting, which sent
// somebody to look at a healthy etcd and fed a verdict to the workflow that
// decided, on the strength of it, to revert an entire epoch out of the
// repository.
//
// This estate already has the rule in the other direction: a guard that cannot
// answer must say so loudly rather than return clean, because "I was not told
// what belongs here" and "nothing is wrong" are identical in an exit code and
// only one is ever true. This is the same rule with the polarity flipped -
// "I could not look" must not be reported as "I looked and it is broken".
type Unavailable struct {
	Tool string
	Why  string
}

func (u *Unavailable) Error() string {
	return fmt.Sprintf("could not check: %s is unavailable (%s)", u.Tool, u.Why)
}
