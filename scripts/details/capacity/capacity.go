// Package capacity is what a site can hold and what holds it: a snapshot of
// the site as its own scheduler sees it, which a change is tried against.
//
// It decides nothing. Whether a change fits is the scheduler's to say, run
// over this snapshot by the engineer; what is here is how the snapshot is
// taken from a cluster and kept where a pull request can read it.
package capacity
