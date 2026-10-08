package repo

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A site rings the watchman exactly as often as the watchman looks round.
//
// How often is one fact held in two places that cannot read each other. The
// watchman's own figure is where the estate declares it, and it is what the
// length of a silence and the vendor's free plan are both worked out from:
// two rings missed is a site gone quiet, and each ring is a write. How often
// a site actually rings is two intervals in its Alertmanager's route. A site
// that rang less often would be called quiet while it was well; one that
// rang more often would spend more of the free plan than the estate counted
// when it refused to go over.
//
// Alertmanager looks at a group every group_interval and sends again once
// repeat_interval has passed. So the ring goes out on the first look at or
// after the repeat - and a repeat that is exactly some number of looks is
// refused, because a look landing a moment early waits a whole look more.
func TestASiteRingsTheWatchmanAsOftenAsItLooksRound(t *testing.T) {
	const receiver = "watchman"
	file, stack := fluxObject(t, kindHelmRelease, "kube-prometheus-stack")
	type route struct {
		Receiver       string  `yaml:"receiver"`
		GroupInterval  string  `yaml:"group_interval"`
		RepeatInterval string  `yaml:"repeat_interval"`
		Routes         []route `yaml:"routes"`
	}
	var found *route
	dec := yaml.NewDecoder(strings.NewReader(stack))
	for {
		var doc struct {
			Spec struct {
				Values struct {
					Alertmanager struct {
						Config struct {
							Route route `yaml:"route"`
						} `yaml:"config"`
					} `yaml:"alertmanager"`
				} `yaml:"values"`
			} `yaml:"spec"`
		}
		if dec.Decode(&doc) != nil {
			break
		}
		for i, child := range doc.Spec.Values.Alertmanager.Config.Route.Routes {
			if child.Receiver == receiver {
				found = &doc.Spec.Values.Alertmanager.Config.Route.Routes[i]
			}
		}
	}
	if found == nil {
		t.Fatalf("%s sends nothing to the %s, so no site rings it and it will never notice one stop", file, receiver)
	}
	minutes := func(what, written string) int {
		m := regexp.MustCompile(`^(\d+)m$`).FindStringSubmatch(written)
		if m == nil {
			t.Fatalf("%s: the route to the %s has a %s of %q, which is not a number of minutes, so how often it rings cannot be worked out",
				file, receiver, what, written)
		}
		n, _ := strconv.Atoi(m[1])
		return n
	}
	looks := minutes("group_interval", found.GroupInterval)
	repeat := minutes("repeat_interval", found.RepeatInterval)
	if repeat%looks == 0 {
		t.Errorf("%s: the route to the %s repeats every %dm and is looked at every %dm, which is exactly %d looks. "+
			"A look that lands a moment early finds the repeat not yet passed and waits a whole look more, so the site "+
			"rings late about as often as not. Make the repeat a little less than the looks it should take.",
			file, receiver, repeat, looks, repeat/looks)
	}
	rings := ((repeat + looks - 1) / looks) * looks

	declaredIn, declared := tofuDeclaring(t, "looks_every_minutes =")
	m := regexp.MustCompile(`(?m)^\s*looks_every_minutes\s*=\s*(\d+)\s*$`).FindStringSubmatch(declared)
	if m == nil {
		t.Fatalf("%s no longer says how often the %s looks round as a plain number of minutes", declaredIn, receiver)
	}
	expected, _ := strconv.Atoi(m[1])
	if rings != expected {
		t.Errorf("a site rings the %s every %d minutes (%s: looked at every %dm, repeated after %dm), and the %s looks "+
			"round every %d (%s). How long a silence is before it is said, and what a site costs on the vendor's free "+
			"plan, are both worked out from the second figure.",
			receiver, rings, file, looks, repeat, receiver, expected, declaredIn)
	}
}
