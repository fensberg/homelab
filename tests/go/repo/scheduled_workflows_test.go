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

// Every scheduled workflow has a deadline, and it is the deadline for how
// often that workflow is asked for.
//
// A workflow that stops running says nothing: its last run stays the newest
// one, and the alert for a failing schedule is content with it. So each has
// an alert for not having run when due. The deadlines differ by how often a
// workflow is asked for, and a workflow named under the wrong one is
// either never late or always late. One that is named under none has no
// deadline at all, which is how it was for all of them.
func TestEveryScheduledWorkflowHasADeadlineForHowOftenItIsAskedFor(t *testing.T) {
	// How often each is asked for, by its most frequent schedule: a cron
	// with a day of the week is weekly, one with an hour is daily, and one
	// with neither is hourly.
	askedFor := map[string]string{}
	rank := map[string]int{"hourly": 0, "daily": 1, "weekly": 2}
	for file, body := range workflowTexts(t) {
		var wf struct {
			On struct {
				Schedule []struct {
					Cron string `yaml:"cron"`
				} `yaml:"schedule"`
			} `yaml:"on"`
		}
		_ = yaml.Unmarshal([]byte(body), &wf)
		for _, s := range wf.On.Schedule {
			fields := strings.Fields(s.Cron)
			if len(fields) != 5 {
				t.Fatalf("%s is scheduled by %q, which is not five fields", file, s.Cron)
			}
			often := "hourly"
			switch {
			case fields[4] != "*":
				often = "weekly"
			case fields[1] != "*":
				often = "daily"
			}
			if was, said := askedFor[file]; !said || rank[often] < rank[was] {
				askedFor[file] = often
			}
		}
	}
	if len(askedFor) == 0 {
		t.Fatal("no workflow was found with a schedule, so this checked nothing")
	}

	const kind, alert = "PrometheusRule", "ScheduledWorkflowHasNotRunWhenDue"
	path, manifest := fluxObject(t, kind, "scheduled-workflows")
	var rule struct {
		Spec struct {
			Groups []struct {
				Rules []struct {
					Alert  string            `yaml:"alert"`
					Expr   string            `yaml:"expr"`
					Labels map[string]string `yaml:"labels"`
				} `yaml:"rules"`
			} `yaml:"groups"`
		} `yaml:"spec"`
	}
	for _, doc := range strings.Split(manifest, "\n---") {
		var probe struct {
			Kind string `yaml:"kind"`
		}
		if yaml.Unmarshal([]byte(doc), &probe) == nil && probe.Kind == kind {
			if err := yaml.Unmarshal([]byte(doc), &rule); err != nil {
				t.Fatalf("reading the %s in %s: %v", kind, path, err)
			}
		}
	}
	named := regexp.MustCompile(`instance=~"([^"]+)"`)
	deadline := map[string]string{}
	for _, g := range rule.Spec.Groups {
		for _, r := range g.Rules {
			if r.Alert != alert {
				continue
			}
			found := named.FindAllStringSubmatch(r.Expr, -1)
			if len(found) == 0 {
				t.Errorf("an alert %s in %s names no workflow, so it is about every one of them at one deadline", alert, path)
			}
			for _, m := range found {
				for _, file := range strings.Split(m[1], "|") {
					if was, twice := deadline[file]; twice && was != r.Labels["asked_for"] {
						t.Errorf("%s is given two deadlines in %s, for %s and for %s", file, path, was, r.Labels["asked_for"])
					}
					deadline[file] = r.Labels["asked_for"]
				}
			}
		}
	}

	var files []string
	for file := range askedFor {
		files = append(files, file)
	}
	sort.Strings(files)
	for _, file := range files {
		switch got := deadline[file]; {
		case got == "":
			t.Errorf("%s is asked for %s and has no deadline: if it stopped running, nothing would say so. "+
				"Name it in the %s alert for %s workflows in %s.", file, askedFor[file], alert, askedFor[file], path)
		case got != askedFor[file]:
			t.Errorf("%s is asked for %s and is held to the deadline for a %s workflow in %s, so it is either never late or always late.",
				file, askedFor[file], got, path)
		}
	}
	for file := range deadline {
		if _, scheduled := askedFor[file]; !scheduled {
			t.Errorf("%s gives %s a deadline, and no workflow of that name runs on a schedule.", path, file)
		}
	}
}
