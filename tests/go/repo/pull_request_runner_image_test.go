package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	supplierslist "homelab/details/suppliers"
	"homelab/details/workorders"
)

// The image a pull request's jobs run in carries every tool those jobs
// install, and nothing that reaches the estate.
//
// WHY. A pool's runner is a machine that runs whatever a pull request says
// to run, so the image it starts from is held from both sides. It has its
// tools in it, so that a job starts on its work: a delivery a job takes that
// the image was not built with is downloaded by every run of that job, on
// the pool's own network, and nothing says the image fell behind. And it has
// none of what the converge's image is for - the vault's client, the
// cluster's, the machines' - because a tool that is there is a tool a pull
// request can run.
//
// FOUND BY WHAT IT IS. The image is whichever one the pools' releases run,
// and its Dockerfile is the one the work order of that name builds. The jobs
// are the ones the suppliers list says are of a pool.

// reachTheEstate is the programs the contractor shells out to that reach
// something of the estate's: the vault, the backups and their key, the
// cluster, and the machines under it.
var reachTheEstate = []string{"op", "age", "rclone", "kubectl", "talosctl"}

var deliveriesTaken = regexp.MustCompile(`take-delivery\.sh((?:\s+(?:\\\n\s*)?[a-z0-9][a-z0-9-]*)+)`)

// takes is every delivery a script asks take-delivery.sh for.
func takes(script string) []string {
	var out []string
	for _, m := range deliveriesTaken.FindAllStringSubmatch(script, -1) {
		out = append(out, strings.Fields(strings.ReplaceAll(m[1], `\`, " "))...)
	}
	return out
}

// instructions is a Dockerfile without its comments, which may say anything.
func instructions(dockerfile string) string {
	var b strings.Builder
	for _, line := range strings.Split(dockerfile, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			b.WriteString(line + "\n")
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
		set := regexp.MustCompile(`(?m)^\s*runnerScaleSetName:\s*(\S+)`).FindStringSubmatch(manifest)
		if set == nil || !pullRequestPools[strings.ReplaceAll(set[1], "${SITE}", "${{ vars.pull_request_runners_site }}")] {
			continue
		}
		m := scaleSetImage.FindStringSubmatch(manifest)
		if m == nil {
			t.Fatalf("the pool %s names no image", release)
		}
		image, _, _ := strings.Cut(m[1], "@")
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

func TestThePullRequestRunnerCarriesWhatItsJobsInstallAndNothingThatReachesTheEstate(t *testing.T) {
	root := repoRoot(t)
	order := pullRequestRunnerOrder(t)
	rel := order.Context + "/Dockerfile"
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	built := instructions(string(body))

	for _, program := range reachTheEstate {
		if regexp.MustCompile(`(^|[^A-Za-z0-9_./-])` + regexp.QuoteMeta(program) + `($|[^A-Za-z0-9_-])`).MatchString(built) {
			t.Errorf("%s, the image a pull request's jobs run in, has %q in it, which reaches the estate.\n\n"+
				"What runs in this image is whatever a pull request says to run. The converge's image is where that program belongs, and no pool runs it.", rel, program)
		}
	}

	carried := map[string]bool{}
	for _, name := range takes(built) {
		carried[name] = true
	}
	if len(carried) == 0 {
		t.Fatalf("%s takes no delivery at all, so it is a bare runner and every job installs everything", rel)
	}

	list, err := os.ReadFile(filepath.Join(root, supplierslist.Path))
	if err != nil {
		t.Fatal(err)
	}
	egress, err := supplierslist.EgressOf(list)
	if err != nil {
		t.Fatal(err)
	}
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
