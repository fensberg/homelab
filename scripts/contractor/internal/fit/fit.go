// Package fit says whether a change leaves room for the work that cannot
// wait.
//
// Inside its machines a site is packed on purpose, and Kubernetes says who
// waits. The one thing that has to fit is work that cannot wait: with any
// one worker gone, everything that is not batch must still have its
// reservation somewhere. Work that can wait is not counted at all, and
// nothing caps the total.
//
// The site as it stands is read from the site, which is the only place
// that knows what is really running on it. What a change would add is read
// from the change. The sum is theirs together:
//
//	what the workers hold
//	- the largest worker, which is the one that may be gone
//	- what work that cannot wait reserves today
//	- what the change adds to that, less what it gives back
//
// Every figure is named in the answer, so that a refusal can be argued
// with.
package fit

import (
	"errors"
	"fmt"
)

const gibibyte = int64(1) << 30

// Site is a site as it stands: what its workers hold for pods, the most any
// one of them holds, and what work that cannot wait reserves on them now.
// Memory, in bytes.
type Site struct {
	Workers       int64
	LargestWorker int64
	MustRun       int64
}

// Change is what a change does to what work that cannot wait reserves: what
// it adds, what it gives back, and the largest single pod it leaves the
// site having to place.
type Change struct {
	Added      int64
	Removed    int64
	LargestPod int64
}

// Line is the whole sum for one site and one change.
type Line struct {
	Site   Site
	Change Change
}

// Of is the sum for a change against a site.
func Of(site Site, change Change) Line { return Line{Site: site, Change: change} }

// WithOneGone is what the workers hold with the largest of them gone.
func (l Line) WithOneGone() int64 { return l.Site.Workers - l.Site.LargestWorker }

// Asked is what work that cannot wait would reserve once the change is in.
func (l Line) Asked() int64 { return l.Site.MustRun + l.Change.Added - l.Change.Removed }

// Short is how far that is past what is left with a worker gone; zero or
// less when it fits.
func (l Line) Short() int64 { return l.Asked() - l.WithOneGone() }

// TooLargeForAWorker says the change leaves a pod no one worker can hold.
func (l Line) TooLargeForAWorker() bool { return l.Change.LargestPod > l.Site.LargestWorker }

// Unread says nothing has read the site, so there is nothing to hold a
// change against.
func (l Line) Unread() bool { return l.Site.Workers <= 0 || l.Site.LargestWorker <= 0 }

// Refused says whether the change is refused: it leaves a pod that fits on
// no worker, or it leaves the site short and asking for more than it
// reserves today.
//
// The second half is what keeps tight from meaning seized, as it does for
// the hypervisor's budget. A site that is already over is not made safer
// by stopping a change that asks for no more, and the change that shrinks
// it is among the ones a flat refusal would block. So such a change goes
// through, and is told the site is over every time.
func (l Line) Refused() bool {
	return l.TooLargeForAWorker() || (l.Short() > 0 && l.Asked() > l.Site.MustRun)
}

// String is the whole sum, in gibibytes.
func (l Line) String() string {
	g := func(b int64) string { return fmt.Sprintf("%.1f", float64(b)/float64(gibibyte)) }
	verdict := "fits, with " + g(-l.Short()) + " to spare"
	switch {
	case l.Short() > 0 && l.Asked() > l.Site.MustRun:
		verdict = "OVER BY " + g(l.Short())
	case l.Short() > 0:
		verdict = "OVER BY " + g(l.Short()) + ", and this change asks for no more than is reserved today, so it is not stopped"
	}
	return fmt.Sprintf("the workers hold %s GiB for pods, and %s with the largest of them gone. Work that cannot wait reserves %s today; "+
		"this change adds %s and gives back %s, which makes %s - %s",
		g(l.Site.Workers), g(l.WithOneGone()), g(l.Site.MustRun), g(l.Change.Added), g(l.Change.Removed), g(l.Asked()), verdict)
}

// Refusal is the refusal for a change that does not fit, with the whole sum
// in it, and nothing for one that does.
func Refusal(l Line) error {
	g := func(b int64) string { return fmt.Sprintf("%.1f", float64(b)/float64(gibibyte)) }
	switch {
	case l.Unread() && l.Change.Added <= l.Change.Removed && l.Change.LargestPod == 0:
		// Nothing is being asked for, so there is nothing to hold against
		// a site nobody has read.
		return nil
	case l.Unread():
		return errors.New("what the site's workers hold has not been read, so this change cannot be held against it. " +
			"That is not the same as fitting")
	case l.TooLargeForAWorker():
		return fmt.Errorf("this change leaves a pod that reserves %s GiB, and no one worker holds more than %s. "+
			"Room spread across the workers is not room for it: %s", g(l.Change.LargestPod), g(l.Site.LargestWorker), l)
	case l.Refused():
		return fmt.Errorf("this change leaves work that cannot wait with no room if a worker is lost: %s.\n\n"+
			"Reserve less, give work that can wait the batch class so it is not counted, or give the site more to hold it with", l)
	}
	return nil
}
