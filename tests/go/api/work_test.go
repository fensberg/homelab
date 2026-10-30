//go:build api

package api_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"homelab/details/repopath"
	"homelab/tests/harness"
)

// How long each job on the estate's runners says it takes is how long it
// takes.
//
// A job says what kind of work it is and how long it ordinarily takes, and
// which runners it may use follows from that (tests/go/repo). A declaration
// is only worth what checks it, and this one can only be checked against
// what happened: so this reads each job's recent successful runs from GitHub
// and holds the middle one of them against what the job says.
//
// It fails both ways. A job that ordinarily takes longer than it says has
// got slow, or was never as quick as claimed. A job that says it takes three
// times longer than it does is taking room in a queue it does not need - and
// if it is over in moments, it could be short.
//
// The middle run and not the longest, because a converge that replaces a
// machine is rare and long and is not what the job ordinarily is; its time
// limit is what bounds that. A job with fewer than three successful runs has
// not yet shown how long it takes, and is said so and not judged.
func TestAJobOnTheEstatesRunnersTakesAsLongAsItSays(t *testing.T) {
	root, err := repopath.Root()
	require.NoError(t, err)
	declared, err := harness.DeclaredWork(root)
	require.NoError(t, err)
	require.NotEmpty(t, declared, "no job says what kind of work it is, so this looked at nothing")
	slug := repoSlug(t)

	for _, w := range declared {
		t.Run(w.Job, func(t *testing.T) {
			says, _ := w.Ordinarily()
			workflow, job, _ := strings.Cut(w.Job, "/")
			named := jobNamed(t, root, workflow, job)

			var runs struct {
				Runs []struct {
					ID int64 `json:"id"`
				} `json:"workflow_runs"`
			}
			require.NoError(t, json.Unmarshal(ghAPI(t, fmt.Sprintf("repos/%s/actions/workflows/%s/runs?status=success&per_page=20", slug, workflow)), &runs))

			var took []time.Duration
			for _, run := range runs.Runs {
				var jobs struct {
					Jobs []struct {
						Name       string    `json:"name"`
						Conclusion string    `json:"conclusion"`
						Started    time.Time `json:"started_at"`
						Completed  time.Time `json:"completed_at"`
					} `json:"jobs"`
				}
				require.NoError(t, json.Unmarshal(ghAPI(t, fmt.Sprintf("repos/%s/actions/runs/%s/jobs?per_page=100", slug, itoa(run.ID))), &jobs))
				for _, j := range jobs.Jobs {
					if j.Conclusion == "success" && named.MatchString(j.Name) {
						took = append(took, j.Completed.Sub(j.Started))
					}
				}
			}
			if len(took) < 3 {
				t.Logf("%s has %d successful run(s) among the last twenty of its workflow, which is too few to say how long it ordinarily takes", w.Job, len(took))
				return
			}
			sort.Slice(took, func(i, j int) bool { return took[i] < took[j] })
			middle := took[len(took)/2]
			if why := misaligned(says, middle); why != "" {
				t.Errorf("%s says it ordinarily takes %s, and over its last %d successful runs the middle one took %s: %s",
					w.Job, says, len(took), middle.Round(time.Second), why)
			}
		})
	}
}

// misaligned says how what a job says it takes and what it took have parted,
// or "" when they agree: it took no longer than it says, and it does not say
// more than three times what it took.
func misaligned(says, took time.Duration) string {
	switch {
	case took > says:
		return "it has got slower than it says, so whatever waits behind it waits longer than it was told. Find out why, or say the longer figure."
	case says > 3*took:
		if took <= harness.ShortAtMost/2 {
			return "it is over far sooner than it says, and soon enough to be short work, which may use the priority runners' idle room."
		}
		return "it is over far sooner than it says. Say what it takes."
	}
	return ""
}

// jobNamed is a pattern for the name GitHub gives a job's runs: the job's
// `name` in its workflow, with each expression in it standing for anything,
// or the job's key where it has no name. A job run for several sites is one
// job here, whichever site a run was for.
func jobNamed(t *testing.T, root, workflow, job string) *regexp.Regexp {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, ".github", "workflows", workflow))
	require.NoError(t, err, "reading the workflow a declared job is in")
	var doc struct {
		Jobs map[string]struct {
			Name string `yaml:"name"`
		} `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	j, ok := doc.Jobs[job]
	require.Truef(t, ok, "%s has no job %s", workflow, job)
	name := j.Name
	if name == "" {
		name = job
	}
	var pattern strings.Builder
	pattern.WriteString("^")
	for i, literal := range regexp.MustCompile(`\$\{\{.*?\}\}`).Split(name, -1) {
		if i > 0 {
			pattern.WriteString(".*")
		}
		pattern.WriteString(regexp.QuoteMeta(literal))
	}
	pattern.WriteString("$")
	return regexp.MustCompile(pattern.String())
}

// The judgement itself, without GitHub: what counts as agreeing.
func TestWhatAJobSaysAndWhatItTookAgreeWithinABand(t *testing.T) {
	for name, c := range map[string]struct {
		says, took time.Duration
		want       string
	}{
		"as it says":                        {5 * time.Minute, 3 * time.Minute, ""},
		"exactly as it says":                {5 * time.Minute, 5 * time.Minute, ""},
		"a third of what it says":           {6 * time.Minute, 2 * time.Minute, ""},
		"slower than it says":               {5 * time.Minute, 6 * time.Minute, "got slower"},
		"far sooner than it says":           {30 * time.Minute, 5 * time.Minute, "Say what it takes"},
		"over in moments and declared long": {10 * time.Minute, 40 * time.Second, "short work"},
	} {
		got := misaligned(c.says, c.took)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: says %s, took %s: got %q, want it to say %q", name, c.says, c.took, got, c.want)
		}
	}
}
