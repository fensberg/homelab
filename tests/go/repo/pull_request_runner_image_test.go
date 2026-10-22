package repo

import (
	"homelab/tests/parsed"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"encoding/json"
	"homelab/details/workorders"
)

// takeDelivery is the one thing that installs a locked tool.
const takeDelivery = "scripts/take-delivery.sh"

// The image a pull request's jobs run in carries exactly what those jobs take
// delivery of: every tool, and nothing else.
//
// WHY EVERY. A pool's runner is ready at once only if its tools are in it. A
// delivery a job takes that the image was not built with is downloaded by
// every run of that job, on the pool's own network, and nothing says the
// image fell behind.
//
// WHY NOTHING ELSE. A tool nobody takes is weight and surface for no work.
// It is not the wall, and this does not pretend it is: a job that wanted a
// tool could fetch it from where the pools are let reach. What holds a pull
// request's job is structural - no credential for anything of the estate's,
// no route to it, nothing kept after it - which is the estate's own rule
// since epoch 01 ("an absent credential is not there to get around"). This
// used to be a list of programs the image might not carry, written by hand;
// the operator agreed on 2026-10-15 to hold the image to what its jobs take
// instead, so that it can be neither more nor less than they need.
//
// FOUND BY WHAT IT IS. The image is whichever one the pools' releases run,
// and its Dockerfile is the one the work order of that name builds. The jobs
// are the ones the suppliers list says are of a pool.

// takes is every delivery a script asks take-delivery.sh for: the words
// after it, in every command that runs it, as the shell parser reads them.
func takes(script string) []string {
	commands, err := parsed.WorkflowCommands("a script", script)
	if err != nil {
		return []string{"(a script that is not shell that can be read: " + err.Error() + ")"}
	}
	delivery := regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	var out []string
	for _, c := range commands {
		at := slices.IndexFunc(c.Words, func(w string) bool { return strings.HasSuffix(w, "take-delivery.sh") })
		if at < 0 {
			continue
		}
		for _, w := range c.Words[at+1:] {
			if delivery.MatchString(w) {
				out = append(out, w)
			}
		}
	}
	return out
}

// dockerInstruction is one instruction of a Dockerfile as Conftest's parser
// reads it: the command, its flags, and its arguments.
type dockerInstruction struct {
	Cmd   string   `json:"Cmd"`
	Flags []string `json:"Flags"`
	Value []string `json:"Value"`
}

// String is the instruction as it would be written, for a message.
func (i dockerInstruction) String() string {
	return strings.TrimSpace(strings.ToUpper(i.Cmd) + " " + strings.Join(append(append([]string{}, i.Flags...), i.Value...), " "))
}

// dockerfile is every instruction of a Dockerfile, without its comments, which
// may say anything.
func dockerfile(t *testing.T, rel string) []dockerInstruction {
	t.Helper()
	return dockerfileIn(t, repository(t), rel)
}

// dockerfileIn is dockerfile, of a file of the given tree.
func dockerfileIn(t *testing.T, tree *parsed.Tree, rel string) []dockerInstruction {
	t.Helper()
	var all []dockerInstruction
	encoded, err := json.Marshal(tree.Conftest(t, rel, "dockerfile"))
	if err == nil {
		err = json.Unmarshal(encoded, &all)
	}
	if err != nil {
		t.Fatalf("%s is not a Dockerfile that can be read: %v", rel, err)
	}
	var out []dockerInstruction
	for _, i := range all {
		if i.Cmd != "comment" {
			out = append(out, i)
		}
	}
	return out
}

// runs is what a Dockerfile's RUN instructions run, as one script.
func runs(instructions []dockerInstruction) string {
	var b strings.Builder
	for _, i := range instructions {
		if i.Cmd == "run" {
			b.WriteString(strings.Join(i.Value, " ") + "\n")
		}
	}
	return b.String()
}

// pullRequestRunnerOrder is the work order that builds the image the pools
// of pull-request runners run: one image for all of them, built here.
func pullRequestRunnerOrder(t *testing.T) workorders.Order {
	t.Helper()
	orders := workOrders(t)
	images := map[string]bool{}
	for _, release := range runnerSets(t) {
		_, manifest := fluxObject(t, kindHelmRelease, release)
		set, ok := scalarAt(manifest, "runnerScaleSetName")
		if !ok || !pullRequestPools[strings.ReplaceAll(set, "${SITE}", "${{ vars.pull_request_runners_site }}")] {
			continue
		}
		named, ok := scalarAt(manifest, "image")
		if !ok {
			t.Fatalf("the pool %s names no image", release)
		}
		image, _, _ := strings.Cut(named, "@")
		images[image] = true
	}
	if len(images) != 1 {
		t.Fatalf("the pools of pull-request runners run %d image(s) (%v). One image is what every pool's jobs are held to; a second is a set of tools nothing here reads.", len(images), images)
	}
	var found workorders.Order
	for image := range images {
		// The longest name that fits: one order's name may end another's.
		for _, o := range orders {
			if o.Context != "" && strings.HasSuffix(image, "-"+o.Name) && len(o.Name) > len(found.Name) {
				found = o
			}
		}
		if found.Name == "" {
			t.Fatalf("the pools run %s, which no work order here builds, so what is in it is not this repository's to say", image)
		}
	}
	return found
}

func TestThePullRequestRunnerCarriesExactlyWhatItsJobsTake(t *testing.T) {
	tree := repository(t)
	order := pullRequestRunnerOrder(t)
	rel := order.Context + "/Dockerfile"
	built := runs(dockerfile(t, rel))

	carried := map[string]bool{}
	for _, name := range takes(built) {
		carried[name] = true
	}
	if len(carried) == 0 {
		t.Fatalf("%s takes no delivery at all, so it is a bare runner and every job installs everything", rel)
	}

	egress := tree.Egress(t)
	jobs, asked := 0, 0
	missing := map[string][]string{}
	for _, e := range egress {
		if e.Pool == "" {
			continue
		}
		jobs++
		workflow, id, _ := strings.Cut(e.Job, "/")
		for _, step := range jobsOf(t, workflow)[id].Steps {
			for _, name := range takes(step.Run) {
				asked++
				if !carried[name] {
					missing[name] = append(missing[name], e.Job)
				}
			}
		}
	}
	took := map[string]bool{}
	for _, e := range egress {
		if e.Pool == "" {
			continue
		}
		workflow, id, _ := strings.Cut(e.Job, "/")
		for _, step := range jobsOf(t, workflow)[id].Steps {
			for _, name := range takes(step.Run) {
				took[name] = true
			}
		}
	}
	// And nothing of the converge's that no job takes, by any way into the
	// image: the system's packages as well as the lock's deliveries. What
	// the contractor shells out to reaches the vault, the backups, the
	// cluster and the machines, and the quickest way to make a pool's job
	// work is to copy in what the converge's image has. A program a job here
	// takes is its own business; OpenTofu's delivery is its installer, which
	// installs tofu (scripts/take-delivery.sh).
	takenPrograms := map[string]bool{}
	for name := range took {
		takenPrograms[name] = true
	}
	if took["opentofu"] {
		takenPrograms["tofu"] = true
	}
	for _, program := range contractorInvokes {
		if takenPrograms[program] {
			continue
		}
		if regexp.MustCompile(`(^|[^A-Za-z0-9_./-])` + regexp.QuoteMeta(program) + `($|[^A-Za-z0-9_-])`).MatchString(built) {
			t.Errorf("%s has %q in it, which reaches the estate and which no job of a pool takes.\n\n"+
				"The converge's image is where that program belongs. What runs in this one is whatever a pull request says to run.", rel, program)
		}
	}

	var unused []string
	for name := range carried {
		if !took[name] {
			unused = append(unused, name)
		}
	}
	sort.Strings(unused)
	for _, name := range unused {
		t.Errorf("%s carries %s, which no job of a pool takes delivery of. Take it out, or it is weight nobody asked for.", rel, name)
	}
	if jobs == 0 || asked == 0 {
		t.Fatalf("read %d job(s) of a pool taking %d delivery(ies), so nothing was checked", jobs, asked)
	}
	names := make([]string, 0, len(missing))
	for name := range missing {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Errorf("%v take delivery of %s, and %s does not carry it.\n\n"+
			"Every run of those jobs would download it on the pool's own network. Add it to the image's take-delivery line.", missing[name], name, rel)
	}

	// What the build reads from outside its own directory is what the order
	// says it reads: the installer and the lock it installs from.
	for _, want := range []string{takeDelivery, "scripts/deliveries.lock"} {
		named := false
		for _, r := range order.Reads {
			named = named || r == want
		}
		if !named {
			t.Errorf("the order %q does not read %s, so the build cannot install from the lock - or installs from a copy of it nothing holds to the original", order.Name, want)
		}
	}
}
