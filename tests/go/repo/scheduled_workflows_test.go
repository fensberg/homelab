package repo

import (
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The cluster is asked about every workflow that runs on a schedule, and
// about nothing else.
//
// A scheduled failure notifies only whoever last edited the cron, by email
// (#441). The cluster asks GitHub for each scheduled workflow's latest run
// and raises an alert while it is one that failed. It is asked by address,
// one for each workflow's file, so both directions are held: a scheduled
// workflow it is not asked about is one whose failures reach nobody, and an
// address for a file that has no schedule is a rename that quietly stopped
// the watch, still spending the hourly allowance on a question with no
// answer.
//
// And the asking fits what GitHub allows a caller with no token: sixty
// requests an hour from one address. More workflows or a shorter interval
// than that allows is refused here, not found out when GitHub starts
// refusing every question.
func TestTheClusterIsAskedAboutEveryScheduledWorkflow(t *testing.T) {
	scheduled := map[string]bool{}
	for file, body := range workflowTexts(t) {
		var wf struct {
			On yaml.Node `yaml:"on"`
		}
		if err := yaml.Unmarshal([]byte(body), &wf); err != nil {
			t.Fatalf("parsing %s: %v", file, err)
		}
		if slices.Contains(triggerNames(&wf.On), "schedule") {
			scheduled[file] = true
		}
	}
	if len(scheduled) == 0 {
		t.Fatal("no workflow runs on a schedule, so this checked nothing: the reader has stopped matching")
	}

	const kind = "ScrapeConfig"
	path, manifest := fluxObject(t, kind, "scheduled-workflows")
	var scrape struct {
		Spec struct {
			ScrapeInterval string `yaml:"scrapeInterval"`
			StaticConfigs  []struct {
				Targets []string `yaml:"targets"`
			} `yaml:"staticConfigs"`
		} `yaml:"spec"`
	}
	for _, doc := range strings.Split(manifest, "\n---") {
		var probe struct {
			Kind string `yaml:"kind"`
		}
		if yaml.Unmarshal([]byte(doc), &probe) == nil && probe.Kind == kind {
			if err := yaml.Unmarshal([]byte(doc), &scrape); err != nil {
				t.Fatalf("reading the %s in %s: %v", kind, path, err)
			}
		}
	}

	asked := map[string]bool{}
	workflowOf := regexp.MustCompile(`/actions/workflows/([^/]+)/runs\?event=schedule&status=completed&per_page=1$`)
	for _, sc := range scrape.Spec.StaticConfigs {
		for _, target := range sc.Targets {
			m := workflowOf.FindStringSubmatch(target)
			if m == nil {
				t.Errorf("%s asks %q, which is not a workflow's latest completed scheduled run. "+
					"Any other question gets an answer the rule does not read.", path, target)
				continue
			}
			asked[m[1]] = true
		}
	}

	var unasked, unknown []string
	for file := range scheduled {
		if !asked[file] {
			unasked = append(unasked, file)
		}
	}
	for file := range asked {
		if !scheduled[file] {
			unknown = append(unknown, file)
		}
	}
	sort.Strings(unasked)
	sort.Strings(unknown)
	for _, file := range unasked {
		t.Errorf("%s runs on a schedule and the cluster is not asked about it, so its failures reach nobody (#441). "+
			"Add its address to the targets in %s.", file, path)
	}
	for _, file := range unknown {
		t.Errorf("%s asks about %s, and no workflow of that name runs on a schedule. "+
			"A renamed workflow drops out of the watch without a word; name it as it is now.", path, file)
	}

	minutes := regexp.MustCompile(`^(\d+)m$`).FindStringSubmatch(scrape.Spec.ScrapeInterval)
	if minutes == nil {
		t.Fatalf("%s asks every %q, which is not a number of minutes, so what it costs GitHub's allowance cannot be worked out",
			path, scrape.Spec.ScrapeInterval)
	}
	every := 0
	for _, d := range minutes[1] {
		every = every*10 + int(d-'0')
	}
	// Half the allowance and no more: the address is the site's, and this is
	// not the only thing at a site that may ask GitHub something with no token.
	const allowed, share = 60, 2
	if anHour := len(asked) * 60 / every; anHour > allowed/share {
		t.Errorf("%s asks GitHub %d times an hour: %d workflows every %d minutes. A caller with no token is allowed %d "+
			"from one address, and this may use half. Ask less often.", path, anHour, len(asked), every, allowed)
	}
}
