package repo

import (
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// jobsOf is a workflow's jobs, with what these tests ask of each.
type jobOf struct {
	Permissions map[string]string `yaml:"permissions"`
	Steps       []struct {
		Name string            `yaml:"name"`
		ID   string            `yaml:"id"`
		If   string            `yaml:"if"`
		Uses string            `yaml:"uses"`
		Run  string            `yaml:"run"`
		Env  map[string]string `yaml:"env"`
		With map[string]string `yaml:"with"`
	} `yaml:"steps"`
}

func jobsOf(t *testing.T, workflow string) map[string]jobOf {
	t.Helper()
	var wf struct {
		Jobs map[string]jobOf `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(workflowText(t, workflow)), &wf); err != nil {
		t.Fatalf("parsing %s: %v", workflow, err)
	}
	if len(wf.Jobs) == 0 {
		t.Fatalf("%s has no jobs this can read", workflow)
	}
	return wf.Jobs
}

var (
	challengeCall = regexp.MustCompile(`(?m)^go run -C scripts/security \. challenge-keys -base "\$BASE" -head "\$HEAD" >>\s*"\$GITHUB_OUTPUT"$`)
	topicFlag     = regexp.MustCompile(`(?m)^\s+-topic ([a-z]+) \\$`)
	// The attestation as a step's own script has it, which starts at the
	// margin; attestationCall reads the workflow's text, where it is indented.
	asksAPerson = regexp.MustCompile(`(?m)^go run -C scripts/attestation \.`)
)

// A key handed to something that did not hold it is asked about on the pull
// request, in a conversation of its own.
//
// WHY THIS EXISTS. Every workflow is a sensitive path, and one reason covers
// the whole directory, so a step gaining a token read exactly as a reworded
// comment did. Security's challenge-keys works out what was handed out; this
// is whether the pull request is actually asked. The step that works it out
// has to run on every pull request and write where the next step reads; the
// step that asks has to run exactly when something was handed out, be keyed
// to what was, and be a different conversation from the sensitive-path one -
// under the same topic each would withdraw the other as its own question
// about content that is gone.
func TestAWidenedKeyIsAskedAboutInItsOwnConversation(t *testing.T) {
	const workflow = "sensitive-paths.yml"
	var challenge, ask, sensitive *struct {
		id, cond, run string
		env           map[string]string
	}
	for _, job := range jobsOf(t, workflow) {
		for _, s := range job.Steps {
			step := &struct {
				id, cond, run string
				env           map[string]string
			}{s.ID, s.If, s.Run, s.Env}
			switch {
			case challengeCall.MatchString(strings.TrimSpace(s.Run)):
				challenge = step
			case asksAPerson.MatchString(s.Run) && strings.Contains(s.Run, "-topic"):
				ask = step
			case asksAPerson.MatchString(s.Run):
				sensitive = step
			}
		}
	}
	if challenge == nil {
		t.Fatal("no step runs security challenge-keys against the pull request's base and head and writes what it finds to the step's outputs, so a key handed out is never worked out")
	}
	if challenge.cond != "" {
		t.Errorf("the step that works out which keys were handed out runs only if %q; it has to run on every pull request", challenge.cond)
	}
	if challenge.id == "" {
		t.Fatal("the step that works out which keys were handed out has no id, so nothing can read what it found")
	}
	if ask == nil || sensitive == nil {
		t.Fatal("the workflow does not open two conversations - one for a sensitive path and one, under a topic of its own, for a key handed out")
	}
	outputs := "steps." + challenge.id + ".outputs."
	if !strings.Contains(ask.cond, outputs+"asked == 'true'") {
		t.Errorf("the step that asks runs if %q, not exactly when a key was handed out", ask.cond)
	}
	for name, from := range map[string]string{"DIGEST": "digest", "ANCHOR": "anchor", "REPORT": "report"} {
		if !strings.Contains(ask.env[name], outputs+from) {
			t.Errorf("the step that asks takes its %s from %q, not from what security found, so the conversation is not keyed to the keys handed out", name, ask.env[name])
		}
	}
	topic := topicFlag.FindStringSubmatch(ask.run)
	if topic == nil || topic[1] == "sensitive" {
		t.Errorf("the conversation about keys is opened under the same topic as the sensitive-path one, so each would withdraw the other as superseded")
	}
}

// The job that runs a branch's own code can write nothing, and the job that
// can write to the pull request runs none of it.
//
// WHY THIS EXISTS. The plan is made by the contractor, built from the branch,
// against roots read from the branch. That step was handed the job's token to
// fetch the release a site runs (#612), in a job that also held
// pull-requests: write for the comment - so a branch's code held a token that
// could write to its own pull request. The comment is its own job now. This
// holds the split: whichever job runs a command of the branch's has a token
// that only reads, and whichever can write checks nothing out and has no
// command of its own at all.
func TestTheJobThatRunsABranchsCodeCannotWrite(t *testing.T) {
	const workflow = "plan-infrastructure.yml"
	plans, writes := 0, 0
	for name, job := range jobsOf(t, workflow) {
		runs := false
		for _, s := range job.Steps {
			if strings.Contains(s.Run, "scripts/contractor") {
				runs = true
			}
		}
		var writable []string
		for scope, level := range job.Permissions {
			if level == "write" {
				writable = append(writable, scope)
			}
		}
		if runs {
			plans++
			if len(job.Permissions) == 0 {
				t.Errorf("the job %s builds and runs the branch's contractor and states no permissions of its own, so its token is whatever the workflow or the repository gives", name)
			}
			if len(writable) > 0 {
				t.Errorf("the job %s builds and runs the branch's contractor with a token that can write %s. What plans only reads; the comment is another job's.", name, strings.Join(writable, ", "))
			}
		}
		if len(writable) == 0 {
			continue
		}
		writes++
		for _, s := range job.Steps {
			if s.Run != "" {
				t.Errorf("the job %s can write %s and runs a command of its own (%q). The job that writes runs only the scripts beside the github-script action.", name, strings.Join(writable, ", "), s.Name)
			}
			if strings.HasSuffix(s.Uses, "/mobilize") && s.With["checkout"] != "false" {
				t.Errorf("the job %s can write %s and checks the branch out. It needs nothing from the repository: the plan arrives as an artifact.", name, strings.Join(writable, ", "))
			}
		}
	}
	if plans == 0 {
		t.Error("no job of the plan workflow runs the contractor, so this has stopped reading the job it guards")
	}
	if writes == 0 {
		t.Error("no job of the plan workflow can write to the pull request, so the plan is never reported")
	}
}
