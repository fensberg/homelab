package repo

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A step that runs the contractor hands it the token it fetches a release
// with.
//
// A site's roots run the release of the platform its line names, and the
// contractor fetches that release from the registry whenever it renders,
// which is before anything it does but sterilize. The registry wants a token
// even for a public package, so a step that does not hand one over is refused
// at Render. The nightly's backup step was such a step: it failed every night
// for five nights, the tests after it never ran, and the patrol that asks
// whether the nightly still completes failed with it.
//
// Sterilize is the one thing the contractor does with nothing rendered, and a
// step that only sterilizes is handed nothing, on purpose.
func TestAStepThatRunsTheContractorHandsItTheTokenItFetchesAReleaseWith(t *testing.T) {
	invocation := regexp.MustCompile(`(?m)toolshed/contractor[ \t]+([a-z][^\n]*)`)
	onlySterilizes := regexp.MustCompile(`-phase[ =]sterilize\b`)
	handsOver := func(envs ...map[string]string) bool {
		for _, env := range envs {
			for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
				if env[name] != "" {
					return true
				}
			}
		}
		return false
	}

	type step struct {
		Name string            `yaml:"name"`
		Run  string            `yaml:"run"`
		Env  map[string]string `yaml:"env"`
	}
	var failures []string
	found := 0
	for _, rel := range trackedFiles(t) {
		if !runnable(rel) {
			continue
		}
		var file struct {
			Env  map[string]string `yaml:"env"`
			Jobs map[string]struct {
				Env   map[string]string `yaml:"env"`
				Steps []step            `yaml:"steps"`
			} `yaml:"jobs"`
			Runs struct {
				Steps []step `yaml:"steps"`
			} `yaml:"runs"`
		}
		if err := yaml.Unmarshal([]byte(readRepoFile(t, rel)), &file); err != nil {
			t.Fatalf("%s does not parse, so its steps were not read: %v", rel, err)
		}
		check := func(where string, jobEnv map[string]string, steps []step) {
			for _, st := range steps {
				for _, m := range invocation.FindAllStringSubmatch(st.Run, -1) {
					found++
					if onlySterilizes.MatchString(m[1]) || handsOver(st.Env, jobEnv, file.Env) {
						continue
					}
					failures = append(failures, rel+where+", step "+strings.TrimSpace(st.Name))
				}
			}
		}
		for name, job := range file.Jobs {
			check(", job "+name, job.Env, job.Steps)
		}
		check("", nil, file.Runs.Steps)
	}
	if found == 0 {
		t.Fatal("found no step that runs the contractor, so nothing was checked: the reader has stopped matching")
	}
	sort.Strings(failures)
	for _, f := range failures {
		t.Errorf("%s runs the contractor and hands it no GitHub token.\n\n"+
			"The contractor fetches the release the site runs before anything it does but sterilize, and "+
			"is refused at Render without a token. Give the step GH_TOKEN, the job's own token.", f)
	}
}
