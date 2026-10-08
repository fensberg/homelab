package repo

import (
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"homelab/tests/harness"
)

// Every job on the estate's runners says what kind of work it is, and is on
// the runners its kind allows.
//
// Priority work is never given room by stopping something. It waits - and
// the promise is that it only ever waits for moments. That holds only while
// nothing long can be on the priority runners that is not itself priority
// work, and a promise like that is broken by the first job somebody points
// at the fast runners because they were free. So a job says which of three
// it is (scripts/approved-suppliers.yml, `work`), and this holds it to that:
//
//   - work that cannot wait, and short work, ask for the priority set;
//   - work that can wait asks for the other, and no other;
//   - short work has a time limit of two minutes or less, which is what
//     makes "it will be done in moments" something GitHub enforces;
//   - nothing says it ordinarily takes longer than its own time limit.
//
// Whether what a job says matches how long it really takes is asked nightly,
// of GitHub (tests/go/api).
func TestEveryJobOnTheEstatesRunnersIsOnTheRunnersItsKindAllows(t *testing.T) {
	declared, err := harness.DeclaredWork(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	said := map[string]harness.Work{}
	for _, w := range declared {
		if _, twice := said[w.Job]; twice {
			t.Errorf("%s says what kind of work it is twice", w.Job)
		}
		said[w.Job] = w
	}

	// Each set of runners, by the name jobs ask for it by, and whether it is
	// the one for work that can wait: the one whose runners are batch.
	type set struct {
		prefix  string
		forWait bool
	}
	var sets []set
	for _, release := range runnerSets(t) {
		_, manifest := fluxObject(t, kindHelmRelease, release)
		prefix, _ := strings.CutSuffix(yamlScalar(t, manifest, "runnerScaleSetName"), "${SITE}")
		var doc struct {
			Spec struct {
				Values struct {
					Template struct {
						Spec struct {
							Priority string `yaml:"priorityClassName"`
						} `yaml:"spec"`
					} `yaml:"template"`
				} `yaml:"values"`
			} `yaml:"spec"`
		}
		if err := yaml.Unmarshal([]byte(manifest), &doc); err != nil {
			t.Fatalf("parsing the runner set %s: %v", release, err)
		}
		sets = append(sets, set{prefix, doc.Spec.Values.Template.Spec.Priority == "batch"})
	}
	sort.Slice(sets, func(i, j int) bool { return len(sets[i].prefix) > len(sets[j].prefix) })

	seen := map[string]bool{}
	for _, wf := range tracked(t, func(rel string) bool {
		return strings.HasPrefix(rel, ".github/workflows/") && strings.HasSuffix(rel, ".yml") && strings.Count(rel, "/") == 2
	}) {
		var workflow struct {
			Jobs map[string]struct {
				RunsOn  yaml.Node `yaml:"runs-on"`
				Timeout int       `yaml:"timeout-minutes"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal([]byte(readRepoFile(t, wf)), &workflow); err != nil {
			t.Fatalf("%s does not parse, so the jobs in it were not read: %v", wf, err)
		}
		for name, job := range workflow.Jobs {
			var on *set
			for i := range sets {
				if job.RunsOn.Kind == yaml.ScalarNode && strings.HasPrefix(job.RunsOn.Value, sets[i].prefix) {
					on = &sets[i]
					break
				}
			}
			if on == nil {
				continue
			}
			key := strings.TrimPrefix(wf, ".github/workflows/") + "/" + name
			seen[key] = true
			w, ok := said[key]
			if !ok {
				t.Errorf(`%s runs on the estate's runners and does not say what kind of work it is.

Add it to the `+"`work`"+` list in scripts/approved-suppliers.yml: whether it
cannot wait, is short, or can wait, and how long it ordinarily takes. Until
it says, nothing can hold it to the runners that keep priority work from
waiting behind it.`, key)
				continue
			}
			takes, _ := w.Ordinarily()
			limit := time.Duration(job.Timeout) * time.Minute
			switch {
			case w.Kind == harness.CanWait && !on.forWait:
				t.Errorf("%s says it can wait and asks for the runners for work that cannot (%s<site>). Work that can wait takes as long as it takes, and on those runners priority work would wait behind it.", key, on.prefix)
			case w.Kind != harness.CanWait && on.forWait:
				t.Errorf("%s says it is %s work and asks for the runners for work that can wait (%s<site>), where it may be stopped part-way and queue behind hours of other work.", key, w.Kind, on.prefix)
			}
			if job.Timeout == 0 {
				t.Errorf("%s has no time limit, so nothing bounds how long it holds a runner", key)
				continue
			}
			if w.Kind == harness.Short && (limit > harness.ShortAtMost || takes > harness.ShortAtMost) {
				t.Errorf("%s says it is short, takes %s and may run for %s. Short work is over in %s or less by its own time limit: that limit is what priority work arriving behind it waits for.", key, takes, limit, harness.ShortAtMost)
			}
			if takes > limit {
				t.Errorf("%s says it ordinarily takes %s and is stopped after %s", key, takes, limit)
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("found no job on the estate's runners, so this looked at nothing")
	}
	for key := range said {
		if !seen[key] {
			t.Errorf("%s says what kind of work it is, and no such job asks for one of the estate's runners", key)
		}
	}
}
