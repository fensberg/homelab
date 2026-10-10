package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"homelab/details/capacity"
	"homelab/details/drafter"
	"homelab/details/gitenv"
)

// One test asks git for a commit's tree, so git is not left to read the
// machine's own configuration.
func TestMain(m *testing.M) {
	gitenv.Isolate()
	os.Exit(m.Run())
}

const gib = int64(1) << 30

// tree writes a repository's worth of files into a directory and returns
// it. Every one is made up: a core, one site, and an application called
// ledgerkeeper that the site may or may not be given. Each file holds its
// documents as the drafter would redraw them, so that nothing here reads a
// format: the engineer does not, and its tests do not need to.
func tree(t *testing.T, files map[string][]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, docs := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("["+strings.Join(docs, ",")+"]"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// deployment is one, as a drawing: how many, at what priority, and what
// each of its containers reserves.
func deployment(name, replicas, priority string, requests ...string) string {
	var containers []string
	for _, r := range requests {
		containers = append(containers, `{"name":"c","resources":{"requests":{"memory":"`+r+`"}}}`)
	}
	pod := `"containers":[` + strings.Join(containers, ",") + `]`
	if priority != "" {
		pod += `,"priorityClassName":"` + priority + `"`
	}
	spec := `"template":{"spec":{` + pod + `}}`
	if replicas != "" {
		spec += `,"replicas":` + replicas
	}
	return `{"kind":"Deployment","metadata":{"name":"` + name + `","namespace":"things"},"spec":{` + spec + `}}`
}

// assigned is the document in a site's file that gives it one application
// with one environment's settings.
const assigned = `{"kind":"Kustomization","metadata":{"name":"ledgerkeeper"},"spec":{"path":"./modules/applications/ledgerkeeper/staging"}}`

// asDrawn stands in for the drafter: every file under what was asked for,
// as it is, and a built directory as every document of every file in it.
func asDrawn(tree string, builds []string, paths ...string) (map[string]json.RawMessage, error) {
	drawn := map[string]json.RawMessage{}
	for _, given := range paths {
		err := filepath.WalkDir(filepath.Join(tree, filepath.FromSlash(given)), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(tree, path)
			drawn[filepath.ToSlash(rel)] = body
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	for _, dir := range builds {
		entries, err := os.ReadDir(filepath.Join(tree, filepath.FromSlash(dir)))
		if err != nil {
			return nil, err
		}
		var all []json.RawMessage
		for _, e := range entries {
			body, err := os.ReadFile(filepath.Join(tree, filepath.FromSlash(dir), e.Name()))
			if err != nil {
				return nil, err
			}
			var docs []json.RawMessage
			if err := json.Unmarshal(body, &docs); err != nil {
				return nil, err
			}
			all = append(all, docs...)
		}
		joined, err := json.Marshal(all)
		if err != nil {
			return nil, err
		}
		drawn[drafter.Built+dir] = joined
	}
	return drawn, nil
}

// What a tree reserves is every pod of every workload it gives the site, by
// what the pod's containers ask for between them.
func TestWhatATreeReservesIsEveryPodOfEveryWorkloadTheSiteIsGiven(t *testing.T) {
	dir := tree(t, map[string][]string{
		"clusters/core/infrastructure/a.yaml": {
			deployment("two-of-them", "2", "interactive", "256Mi", "256Mi"),
			deployment("says-nothing-of-how-many", "", "", "1Gi"),
			deployment("can-wait", "4", "batch", "4Gi"),
			`{"kind":"DaemonSet","metadata":{"name":"on-each","namespace":"things"},"spec":{"template":{"spec":{"containers":[{"name":"c","resources":{"requests":{"memory":"128Mi"}}}]}}}}`,
			`{"kind":"HelmRelease","metadata":{"name":"chart","namespace":"things"},"spec":{"values":{
			  "server":{"replicaCount":2,"resources":{"requests":{"memory":"512Mi"}}},
			  "worker":{"priorityClassName":"batch","resources":{"requests":{"memory":"8Gi"}}}}}}`,
		},
		"clusters/core/versions.env":                          {`"not a manifest, and the drafter redraws it all the same"`},
		"clusters/north/applications.yaml":                    {assigned},
		"clusters/south/extra.yaml":                           {deployment("another-sites", "9", "", "1Gi")},
		"modules/applications/ledgerkeeper/staging/it.yaml":   {deployment("ledgerkeeper", "1", "interactive", "3Gi")},
		"modules/applications/unassigned/staging/other.yaml":  {deployment("not-given", "5", "", "1Gi")},
		"modules/applications/ledgerkeeper/production/x.yaml": {deployment("ledgerkeeper", "7", "", "9Gi")},
	})
	got, err := reserved(dir, "north", 3, asDrawn)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{
		"Deployment/things/two-of-them":              1 * gib,
		"Deployment/things/says-nothing-of-how-many": 1 * gib,
		"Deployment/things/can-wait":                 0,
		"DaemonSet/things/on-each":                   3 * 128 << 20,
		"HelmRelease/things/chart.server":            1 * gib,
		"HelmRelease/things/chart.worker":            0,
		"Deployment/things/ledgerkeeper":             3 * gib,
	}
	for name, asks := range want {
		w, ok := got[name]
		if !ok {
			t.Errorf("%s reserves and was not found", name)
			continue
		}
		if w.asks() != asks {
			t.Errorf("%s asks for %d bytes, not %d", name, w.asks(), asks)
		}
	}
	if len(got) != len(want) {
		t.Errorf("found %d things that reserve, and the site is given %d: %v", len(got), len(want), got)
	}
}

// A size that cannot be read stops the count and names what wrote it. It is
// never counted as nothing: that is a workload let through for free.
func TestAReservationThatCannotBeReadStopsTheCountAndIsNamed(t *testing.T) {
	dir := tree(t, map[string][]string{"clusters/core/a.yaml": {deployment("unreadable", "1", "", "lots")}})
	_, err := reserved(dir, "north", 3, asDrawn)
	if err == nil || !strings.Contains(err.Error(), "Deployment/things/unreadable") {
		t.Fatalf("a size that cannot be read was counted, or what wrote it was not named: %v", err)
	}
	refuses := func(string, []string, ...string) (map[string]json.RawMessage, error) {
		return nil, errors.New("a.yaml: not YAML that can be read")
	}
	if _, err := reserved(dir, "north", 3, refuses); err == nil || !strings.Contains(err.Error(), "a.yaml") {
		t.Errorf("a file the drafter could not redraw was read as one that reserves nothing: %v", err)
	}
	notThere := tree(t, map[string][]string{"clusters/north/applications.yaml": {assigned}})
	if _, err := reserved(notThere, "north", 3, asDrawn); err == nil || !strings.Contains(err.Error(), "ledgerkeeper") {
		t.Errorf("an application whose directory is not there was counted as reserving nothing: %v", err)
	}
}

// What a change adds is what is asked for now and was not before, and what
// it gives back the other way about. Work that can wait is neither.
func TestAChangeIsWhatIsAskedForNowAndWasNotBefore(t *testing.T) {
	before := map[string]workload{
		"grows":      {Pods: 1, PodBytes: 1 * gib},
		"shrinks":    {Pods: 2, PodBytes: 2 * gib},
		"goes":       {Pods: 1, PodBytes: 3 * gib},
		"untouched":  {Pods: 1, PodBytes: 6 * gib},
		"waits-more": {Pods: 1, PodBytes: 1 * gib, CanWait: true},
	}
	after := map[string]workload{
		"grows":      {Pods: 3, PodBytes: 1 * gib},
		"shrinks":    {Pods: 2, PodBytes: 1 * gib},
		"untouched":  {Pods: 1, PodBytes: 6 * gib},
		"arrives":    {Pods: 1, PodBytes: 5 * gib},
		"waits-more": {Pods: 9, PodBytes: 9 * gib, CanWait: true},
	}
	got, grew := change(before, after)
	// grows by two, and five arrive; shrinks by two, and three go.
	if got.Added != 7*gib || got.Removed != 5*gib {
		t.Errorf("added %d GiB and gave back %d, not seven and five", got.Added/gib, got.Removed/gib)
	}
	// The largest pod among what is new or was altered: not the six that
	// was already there, and not the nine that can wait.
	if got.LargestPod != 5*gib {
		t.Errorf("the largest pod to place is %d GiB, not five", got.LargestPod/gib)
	}
	if strings.Join(grew, ",") != "arrives,grows" {
		t.Errorf("what asks for more than it did is %v", grew)
	}
}

// record is a site's record with its capacity in it, or with none.
func record(t *testing.T, site *capacity.Site) string {
	t.Helper()
	dir := t.TempDir()
	if site != nil {
		raw, err := capacity.Marshal(*site, "2026-10-11T04:00:00Z")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, capacity.File), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func stampOf(t *testing.T, before, after map[string][]string, site *capacity.Site) (int, string) {
	t.Helper()
	was, now := tree(t, before), tree(t, after)
	var out, complaints bytes.Buffer
	code := stamp([]string{"-site", "north", "-record", record(t, site), "-base", "the-commit-before"}, &out, &complaints, now,
		func(_, revision string) (string, func(), error) {
			if revision != "the-commit-before" {
				t.Errorf("the tree was asked for at %q, not at the commit the change is from", revision)
			}
			return was, func() {}, nil
		}, asDrawn)
	return code, out.String() + complaints.String()
}

// A site of three workers holding nine each, with eleven reserved.
var north = capacity.Site{Machines: 3, Workers: 27 * gib, LargestWorker: 9 * gib, MustRun: 11 * gib}

// A change is stamped when what it adds still fits with a worker gone, and
// refused when it does not, with what grew named.
func TestAChangeIsStampedWhenItFitsAndRefusedWhenItDoesNot(t *testing.T) {
	core := "clusters/core/a.yaml"
	code, said := stampOf(t,
		map[string][]string{core: {deployment("thing", "1", "", "1Gi")}},
		map[string][]string{core: {deployment("thing", "8", "", "1Gi")}}, &north)
	if code != 0 || said != "" {
		t.Errorf("seven more on eleven, in eighteen, was not stamped in silence (%d):\n%s", code, said)
	}
	code, said = stampOf(t,
		map[string][]string{core: {deployment("thing", "1", "", "1Gi")}},
		map[string][]string{core: {deployment("thing", "9", "", "1Gi")}}, &north)
	if code != 1 || !strings.Contains(said, "REFUSED") || !strings.Contains(said, "OVER BY 1.0") || !strings.Contains(said, "Deployment/things/thing") {
		t.Errorf("eight more on eleven, in eighteen, was not refused with what grew named (%d):\n%s", code, said)
	}
	code, said = stampOf(t,
		map[string][]string{core: {deployment("thing", "1", "", "1Gi")}},
		map[string][]string{core: {deployment("thing", "1", "", "1Gi"), deployment("more-of-what-waits", "20", "batch", "4Gi")}}, &north)
	if code != 0 || said != "" {
		t.Errorf("eighty gibibytes of work that can wait was held against what cannot, or something was said of it (%d):\n%s", code, said)
	}
}

// A site nobody has read cannot be held to anything. A change that asks for
// something is refused for it; one that asks for nothing is not.
func TestAChangeAgainstASiteNobodyHasReadIsRefusedOnlyIfItAsksForSomething(t *testing.T) {
	core := "clusters/core/a.yaml"
	same := map[string][]string{core: {deployment("thing", "1", "", "1Gi")}}
	if code, said := stampOf(t, same, same, nil); code != 0 || said != "" {
		t.Errorf("a change that asks for nothing was not let through an unread site in silence (%d):\n%s", code, said)
	}
	code, said := stampOf(t, same, map[string][]string{core: {deployment("thing", "2", "", "1Gi")}}, nil)
	if code != 1 || !strings.Contains(said, "REFUSED") || !strings.Contains(said, "is not on record") {
		t.Errorf("a change that asks for more was let through a site nobody has read, or not told why (%d):\n%s", code, said)
	}
}

// A commit's tree is taken out of the repository as it was at that commit,
// and only the parts a site's work is declared in.
func TestACommitsTreeIsTakenOutAsItWasThen(t *testing.T) {
	repo := tree(t, map[string][]string{
		"clusters/core/a.yaml": {deployment("thing", "1", "", "1Gi")},
		"docs/unrelated.md":    {`"not a site's work"`},
	})
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("add", "-A")
	git("-c", "user.name=a test", "-c", "user.email=test@example.invalid", "commit", "-q", "-m", "as it was")
	// And then it changes, in the working tree only.
	if err := os.WriteFile(filepath.Join(repo, "clusters", "core", "a.yaml"), []byte("["+deployment("thing", "5", "", "1Gi")+"]"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir, done, err := archived(repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	got, err := reserved(dir, "north", 3, asDrawn)
	if err != nil {
		t.Fatal(err)
	}
	if got["Deployment/things/thing"].Pods != 1 {
		t.Errorf("the commit's tree was read as the working tree: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs")); err == nil {
		t.Error("more than a site's work was taken out of the commit")
	}
	if _, _, err := archived(repo, "no-such-commit"); err == nil {
		t.Error("a commit that does not exist gave a tree")
	}
}
