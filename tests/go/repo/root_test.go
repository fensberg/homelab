package repo

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Every job says whether it keeps root, and no job claims a protection its
// runner does not give.
//
// WHY ROOT. GitHub-hosted runners give the runner account passwordless sudo,
// and harden-runner enforces egress with an agent and firewall rules that root
// can stop or flush. So in a job that keeps sudo, a later step - the compromised
// dependency the block exists for - can switch the block off first (#410).
// mobilize forwards disable-sudo-and-containers and requires every job to state
// it; this requires the statement to be one the repository can stand behind.
//
// WHY RUNNERS. harden-runner returns early on the self-hosted ARC runner, where
// it only writes its policy to stdout for a StepSecurity agent this estate does
// not run, and inside a job container. There, `block` blocks nothing and
// removing sudo removes nothing (#411). A job claiming either would be a status
// that says something untrue, on exactly the jobs holding the vault token.
//
// WHERE "SELF-HOSTED" COMES FROM. The labels in .github/actionlint.yaml, which
// already have to name every self-hosted runner or a typo in runs-on fails at
// commit time. Reading that declaration rather than listing runners here means
// a new scale set is covered the moment it is usable.

type egressRoot struct {
	Egress []struct {
		Job    string `yaml:"job"`
		Policy string `yaml:"policy"`
		Root   string `yaml:"root"`
	} `yaml:"egress"`
}

func TestEveryJobSaysWhetherItKeepsRoot(t *testing.T) {
	root := intendedRoot(t)

	selfHosted := selfHostedLabels(t, root)

	var suppliers egressRoot
	body, err := os.ReadFile(filepath.Join(root, "scripts", "approved-suppliers.yml"))
	if err != nil {
		t.Fatalf("reading scripts/approved-suppliers.yml: %v", err)
	}
	if err := yaml.Unmarshal(body, &suppliers); err != nil {
		t.Fatalf("parsing scripts/approved-suppliers.yml: %v", err)
	}
	declared := map[string]struct{ policy, root string }{}
	for _, e := range suppliers.Egress {
		declared[e.Job] = struct{ policy, root string }{e.Policy, strings.TrimSpace(e.Root)}
	}

	jobs, disabled := 0, 0
	for _, rel := range trackedFilesIn(t, root) {
		if !authoredHere(rel) || !runnable(rel) || filepath.Base(rel) == "action.yml" {
			continue
		}
		wf, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		var doc struct {
			Jobs map[string]struct {
				RunsOn    any              `yaml:"runs-on"`
				Container any              `yaml:"container"`
				Steps     []map[string]any `yaml:"steps"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(wf, &doc); err != nil {
			t.Fatalf("parsing %s: %v", rel, err)
		}
		var names []string
		for n := range doc.Jobs {
			names = append(names, n)
		}
		sort.Strings(names)

		for _, n := range names {
			job := doc.Jobs[n]
			key := filepath.Base(rel) + "/" + n
			jobs++

			var with map[string]any
			for _, step := range job.Steps {
				if uses, _ := step["uses"].(string); uses == "$/.github/workflows/mobilize" {
					with, _ = step["with"].(map[string]any)
					break
				}
			}
			if with == nil {
				t.Errorf("%s does not start with $/.github/workflows/mobilize, so nothing says whether it keeps root", key)
				continue
			}
			value, _ := with["disable-sudo-and-containers"].(string)
			policy, _ := with["egress-policy"].(string)

			inert := ""
			if job.Container != nil {
				inert = "runs in a job container"
			} else if label := runsOnLabel(job.RunsOn); selfHosted[label] {
				inert = "runs on the self-hosted runner " + label
			}

			switch {
			case value == "":
				t.Errorf(`%s does not say whether it keeps root.

Give mobilize disable-sudo-and-containers: "true" - or "false" with the reason
declared as root: beside the job's egress entry. With sudo, any later step can
stop harden-runner's agent and then call out (#410).`, key)
				continue
			case value != "true" && value != "false":
				t.Errorf(`%s gives disable-sudo-and-containers %q, which is neither "true" nor "false".`, key, value)
				continue
			}

			if inert != "" {
				if value == "true" {
					t.Errorf(`%s %s and claims disable-sudo-and-containers: "true".

harden-runner returns before acting there, so sudo is not removed. A setting
that reads as a protection and does nothing is worse than none (#411). Say
"false".`, key, inert)
				}
				if policy == "block" {
					t.Errorf(`%s %s and claims egress-policy: block.

harden-runner blocks nothing there - on the ARC runner it only writes its policy
for a cluster agent this estate does not run - so the job would read as
restricted while reaching anything (#411). Say audit until egress for that
runner is controlled somewhere that enforces it.`, key, inert)
				}
				continue
			}

			reason := declared[key].root
			if value == "true" {
				disabled++
				if reason != "" {
					t.Errorf(`%s removes sudo and still declares a root: reason in
scripts/approved-suppliers.yml.

A reason for an exception that is no longer taken is a standing permission
nobody is using, waiting for the next change to inherit it. Remove it.`, key)
				}
				continue
			}
			if reason == "" {
				t.Errorf(`%s keeps root with no reason declared.

It runs on a GitHub-hosted runner where harden-runner does act, so leaving sudo
in place lets any later step in the job switch egress enforcement off (#410).
If the job genuinely needs root or Docker, say why as root: beside its egress
entry in scripts/approved-suppliers.yml. Otherwise give mobilize
disable-sudo-and-containers: "true".`, key)
			}
		}
	}

	const fewestJobs = 10
	if jobs < fewestJobs {
		t.Fatalf("only %d job(s) were read, so this checked almost nothing", jobs)
	}
	if disabled == 0 {
		t.Fatal("no job removes sudo, which means the read is wrong or #410 has been undone everywhere")
	}
}

// selfHostedLabels reads the runner labels .github/actionlint.yaml declares.
func selfHostedLabels(t *testing.T, root string) map[string]bool {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, ".github", "actionlint.yaml"))
	if err != nil {
		t.Fatalf("reading .github/actionlint.yaml: %v", err)
	}
	var doc struct {
		SelfHosted struct {
			Labels []string `yaml:"labels"`
		} `yaml:"self-hosted-runner"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parsing .github/actionlint.yaml: %v", err)
	}
	if len(doc.SelfHosted.Labels) == 0 {
		t.Fatal(".github/actionlint.yaml declares no self-hosted runner, and the deploy jobs use one - the read has stopped matching")
	}
	out := map[string]bool{}
	for _, l := range doc.SelfHosted.Labels {
		out[l] = true
	}
	return out
}

// runsOnLabel returns the label a job's runs-on names, for the single-label
// form every job here uses. A list or a group returns empty, which is treated
// as GitHub-hosted - the stricter of the two readings.
func runsOnLabel(v any) string {
	s, _ := v.(string)
	return s
}
