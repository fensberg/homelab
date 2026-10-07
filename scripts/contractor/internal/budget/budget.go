// Package budget says whether a hypervisor can hold the machines a site asks
// of it.
//
// The hypervisor is strict. Inside the machines a site is packed past what
// would fit if everything peaked at once, on purpose, because Kubernetes can
// say who waits; a hypervisor that runs out of memory kills a machine and
// says nothing first. So the machines' memory never adds up to more than the
// host can give, and what the host can give is worked out, not hoped:
//
//	what the host has
//	- what the hypervisor keeps for itself
//	- what its filesystem may hold as cache
//	- what machines that are not the site's are given
//	- one control plane's worth, so a machine can be replaced by a new one
//	  before the old one is gone
//
// Every figure but the first is a deduction somebody chose, and each is
// named in the answer so a refusal can be argued with.
package budget

import (
	"fmt"
	"sort"
	"strings"

	"homelab/contractor/config"
	"homelab/details/hypervisorapi"
)

const gibibyte = int64(1) << 30

// Kept is what a hypervisor keeps for itself: its own services, and room
// for the bookkeeping each machine costs it.
const Kept = 2 * gibibyte

// Cache is what the hypervisor's filesystem may hold in memory: a tenth of
// the host, and never more than sixteen gibibytes, which is the hypervisor's
// own default for a new install. Counted as memory the machines cannot have,
// because it is given back slowly and a host that runs short before it is
// given back kills a machine.
func Cache(host int64) int64 {
	if tenth := host / 10; tenth < 16*gibibyte {
		return tenth
	}
	return 16 * gibibyte
}

// Line is one hypervisor's budget: what it has, each thing taken off it, and
// what the site asks.
type Line struct {
	Hypervisor string
	Host       int64
	Kept       int64
	Cache      int64
	Others     int64
	Reserve    int64
	Asked      int64
	// Standing is what the site's machines already on the host are given.
	Standing int64
}

// ForTheSite is what is left for the site's machines.
func (l Line) ForTheSite() int64 { return l.Host - l.Kept - l.Cache - l.Others - l.Reserve }

// Short is how far what the site asks is past what is left; zero or less
// when it fits.
func (l Line) Short() int64 { return l.Asked - l.ForTheSite() }

// Refused says whether the site is refused this hypervisor: it asks more
// than is left, and more than it already has there.
//
// The second half is what keeps tight from meaning seized. A site that is
// over and asking for no more than it stands on is not made safer by being
// stopped: the machines are already there, and the converge that would
// shrink them is the one a flat refusal blocks. So a site may always
// converge to what it has or to less, however far over that is, and is
// told that it is over every time it does.
func (l Line) Refused() bool { return l.Short() > 0 && l.Asked > l.Standing }

// String is the whole sum, in gibibytes, so whoever reads a refusal can see
// which figure to question.
func (l Line) String() string {
	g := func(b int64) string { return fmt.Sprintf("%.1f", float64(b)/float64(gibibyte)) }
	verdict := "fits, with " + g(-l.Short()) + " to spare"
	switch {
	case l.Refused():
		verdict = "OVER BY " + g(l.Short())
	case l.Short() > 0:
		verdict = "OVER BY " + g(l.Short()) + ", and asking no more than the " + g(l.Standing) + " it already has there, so it is not stopped"
	}
	return fmt.Sprintf("%s has %s GiB: %s kept by the hypervisor, %s for its cache, %s given to machines that are not this site's, %s held to replace a machine with; %s for the site, whose machines ask %s - %s",
		l.Hypervisor, g(l.Host), g(l.Kept), g(l.Cache), g(l.Others), g(l.Reserve), g(l.ForTheSite()), g(l.Asked), verdict)
}

// Of is the budget of every hypervisor the site puts a machine on.
//
// hosts is what each hypervisor has, by its key; site is the site as
// planned. A machine on a hypervisor nobody read is an error: there is
// nothing to hold it against. A machine already on a host that is not one of
// the site's is somebody else's, and its memory is not the site's to have -
// except a template, which is never started.
func Of(hosts map[string]hypervisorapi.Host, site *config.SiteNetwork) ([]Line, error) {
	// The site's own are the ids it plans, and any other id in the band its
	// octet gives it: a machine the plan no longer asks for is still the
	// site's while it stands, and counting it as somebody else's would hold
	// a site's own shrinking against it.
	planned := map[int]bool{}
	for _, id := range site.Reserved {
		planned[id] = true
	}
	mine := func(id int) bool {
		return planned[id] || (site.Octet != 0 && id/1000 == site.Octet)
	}
	asked := map[string]int64{}
	for _, m := range site.Machines {
		asked[m.Hypervisor] += m.MemoryBytes
	}
	keys := make([]string, 0, len(asked))
	for key := range asked {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	reserve := config.MemoryOf[config.ControlPlane]
	var lines []Line
	for _, key := range keys {
		host, ok := hosts[key]
		if !ok {
			return nil, fmt.Errorf("the site puts machines on the hypervisor %q, and nothing read what that hypervisor has", key)
		}
		line := Line{Hypervisor: key, Host: host.MemoryBytes, Kept: Kept, Cache: Cache(host.MemoryBytes), Reserve: reserve, Asked: asked[key]}
		for _, m := range host.Machines {
			switch {
			case m.Template:
			case mine(m.ID):
				line.Standing += m.MemoryBytes
			default:
				line.Others += m.MemoryBytes
			}
		}
		lines = append(lines, line)
	}
	return lines, nil
}

// Refusal is the error for a site whose machines a hypervisor cannot hold,
// or nil when every one fits.
func Refusal(lines []Line) error {
	var over []string
	for _, l := range lines {
		if l.Refused() {
			over = append(over, "  "+l.String())
		}
	}
	if len(over) == 0 {
		return nil
	}
	return fmt.Errorf(`the site asks its hypervisor for more memory than it can give:

%s

A hypervisor that runs out of memory kills a machine, so this is refused
before anything is built. Give the site fewer or smaller machines, or free
what is counted against it: every figure above is named so it can be
questioned`, strings.Join(over, "\n"))
}
