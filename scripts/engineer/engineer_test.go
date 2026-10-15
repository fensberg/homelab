package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"homelab/details/flux"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

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
const assigned = `{"kind":"Kustomization","metadata":{"name":"ledgerkeeper"},"spec":{"path":"./modules/applications/ledgerkeeper/staging","targetNamespace":"ledgerkeeper"}}`

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

// What a tree asks a site for: the pods of an application it was given, as
// its manifests make them, and what everything else reserves.
func TestWhatATreeAsksForIsAnApplicationsPodsAndWhatEverythingElseReserves(t *testing.T) {
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
		"clusters/core/versions.env":       {`"not a manifest, and the drafter redraws it all the same"`},
		"clusters/north/applications.yaml": {assigned},
		"clusters/south/extra.yaml":        {deployment("another-sites", "9", "", "1Gi")},
		"modules/applications/ledgerkeeper/staging/it.yaml": {
			deployment("ledgerkeeper", "2", "interactive", "3Gi"),
			deployment("its-sweeper", "3", "batch", "1Gi"),
			`{"kind":"PersistentVolumeClaim","metadata":{"name":"books"},"spec":{"storageClassName":"kept","resources":{"requests":{"storage":"5Gi"}}}}`,
			`{"kind":"StatefulSet","metadata":{"name":"vault"},"spec":{"replicas":2,"volumeClaimTemplates":[{"metadata":{"name":"data"},"spec":{"resources":{"requests":{"storage":"1Gi"}}}}],
			  "template":{"metadata":{"labels":{"part":"vault"}},"spec":{"priorityClassName":"interactive","containers":[{"name":"c","image":"images.invalid/v:1","resources":{"requests":{"memory":"1Gi"}}}],
			  "volumes":[{"name":"b","persistentVolumeClaim":{"claimName":"books"}}]}}}}`,
			`{"kind":"CronJob","metadata":{"name":"nightly"},"spec":{"jobTemplate":{"spec":{"template":{"spec":{"containers":[{"name":"c","resources":{"requests":{"memory":"64Mi"}}}]}}}}}}`,
		},
		"modules/applications/unassigned/staging/other.yaml":  {deployment("not-given", "5", "", "1Gi")},
		"modules/applications/ledgerkeeper/production/x.yaml": {deployment("ledgerkeeper", "7", "", "9Gi")},
	})
	got, err := asked(dir, "north", 3, asDrawn)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]workload{
		"Deployment/things/two-of-them":              {Pods: 2, PodBytes: 512 << 20},
		"Deployment/things/says-nothing-of-how-many": {Pods: 1, PodBytes: gib},
		"Deployment/things/can-wait":                 {Pods: 4, PodBytes: 4 * gib, CanWait: true},
		"DaemonSet/things/on-each":                   {Pods: 3, PodBytes: 128 << 20},
		"HelmRelease/things/chart.server":            {Pods: 2, PodBytes: 512 << 20},
		"HelmRelease/things/chart.worker":            {Pods: 1, PodBytes: 8 * gib, CanWait: true},
		"CronJob/<nil>/nightly":                      {Pods: 1, PodBytes: 64 << 20},
	}
	for name, w := range want {
		if got.Core[name] != w {
			t.Errorf("%s is %+v, not %+v", name, got.Core[name], w)
		}
	}
	if len(got.Core) != len(want) {
		t.Errorf("found %d things that reserve outside an application's own pods, not %d: %v", len(got.Core), len(want), got.Core)
	}

	// Two pods of the deployment and two of the set, each its own; none of
	// the work that can wait.
	var pods []string
	for _, p := range got.Pods {
		pods = append(pods, p.Namespace+" "+podName(p))
	}
	if strings.Join(pods, ", ") != "ledgerkeeper deployment-ledgerkeeper-0, ledgerkeeper deployment-ledgerkeeper-1, ledgerkeeper statefulset-vault-0, ledgerkeeper statefulset-vault-1" {
		t.Fatalf("the application's pods are %v", pods)
	}
	second, _ := json.Marshal(got.Pods[3])
	for _, held := range []string{`"claimName":"books"`, `"claimName":"data-vault-1"`, `"part":"vault"`, `"priorityClassName":"interactive"`, `"memory":"1Gi"`} {
		if !strings.Contains(string(second), held) {
			t.Errorf("the set's second pod does not hold %s: %s", held, second)
		}
	}
	if strings.Contains(string(second), "images.invalid") {
		t.Errorf("the pod carries more than a scheduler reads: %s", second)
	}
	var claims []string
	for _, c := range got.Claims {
		claims = append(claims, c.Namespace+"/"+c.Name)
	}
	if strings.Join(claims, " ") != "ledgerkeeper/books ledgerkeeper/data-vault-0 ledgerkeeper/data-vault-1" {
		t.Errorf("the application's claims are %v", claims)
	}
}

// A size that cannot be read stops the count and names what wrote it. It is
// never counted as nothing: that is a workload let through for free.
func TestAReservationThatCannotBeReadStopsTheCountAndIsNamed(t *testing.T) {
	dir := tree(t, map[string][]string{"clusters/core/a.yaml": {deployment("unreadable", "1", "", "lots")}})
	_, err := asked(dir, "north", 3, asDrawn)
	if err == nil || !strings.Contains(err.Error(), "Deployment/things/unreadable") {
		t.Fatalf("a size that cannot be read was counted, or what wrote it was not named: %v", err)
	}
	refuses := func(string, []string, ...string) (map[string]json.RawMessage, error) {
		return nil, errors.New("a.yaml: not YAML that can be read")
	}
	if _, err := asked(dir, "north", 3, refuses); err == nil || !strings.Contains(err.Error(), "a.yaml") {
		t.Errorf("a file the drafter could not redraw was read as one that reserves nothing: %v", err)
	}
	notThere := tree(t, map[string][]string{"clusters/north/applications.yaml": {assigned}})
	if _, err := asked(notThere, "north", 3, asDrawn); err == nil || !strings.Contains(err.Error(), "ledgerkeeper") {
		t.Errorf("an application whose directory is not there was counted as reserving nothing: %v", err)
	}
	inApp := tree(t, map[string][]string{"clusters/north/applications.yaml": {assigned},
		"modules/applications/ledgerkeeper/staging/it.yaml": {deployment("ledgerkeeper", "1", "", "lots")}})
	if _, err := asked(inApp, "north", 3, asDrawn); err == nil || !strings.Contains(err.Error(), "Deployment ledgerkeeper") {
		t.Errorf("an application's pod reserving a size that cannot be read was drawn, or not named: %v", err)
	}
}

// north is a site of a control plane and two workers. One worker runs a
// pod of the application, a pod of a set that runs on every machine and a
// job that can wait; the other runs one pod of something in the core.
func north(t *testing.T) capacity.Snapshot {
	t.Helper()
	pod := func(namespace, name, machine, class, owner, ownerName, memory string) string {
		return `{"metadata":{"namespace":"` + namespace + `","name":"` + name + `","ownerReferences":[{"kind":"` + owner + `","name":"` + ownerName + `"}]},
		 "spec":{"nodeName":"` + machine + `","priorityClassName":"` + class + `","containers":[{"name":"c","resources":{"requests":{"memory":"` + memory + `"}}}]},"status":{"phase":"Running"}}`
	}
	s, err := capacity.Take(capacity.Listings{
		Nodes: []byte(`{"items":[
 {"metadata":{"name":"example-cp-100","labels":{"node-role.kubernetes.io/control-plane":""}},"status":{"allocatable":{"memory":"4Gi"}}},
 {"metadata":{"name":"example-wk-200","labels":{}},"status":{"allocatable":{"memory":"8Gi"}}},
 {"metadata":{"name":"example-wk-201","labels":{}},"status":{"allocatable":{"memory":"8Gi"}}}]}`),
		Namespaces:      []byte(`{"items":[{"metadata":{"name":"ledgerkeeper"}},{"metadata":{"name":"things"}}]}`),
		PriorityClasses: []byte(capacity.Nothing), StorageClasses: []byte(capacity.Nothing), Volumes: []byte(capacity.Nothing),
		Claims: []byte(`{"items":[{"metadata":{"namespace":"ledgerkeeper","name":"books"},"spec":{"volumeName":"v"}}]}`),
		Pods: []byte(`{"items":[` + strings.Join([]string{
			pod("ledgerkeeper", "ledgerkeeper-7c9-aaaaa", "example-wk-200", "interactive", "ReplicaSet", "ledgerkeeper-7c9", "3Gi"),
			pod("things", "on-each-x", "example-wk-200", "interactive", "DaemonSet", "on-each", "128Mi"),
			pod("things", "sweep-1", "example-wk-200", "batch", "Job", "sweep", "6Gi"),
			pod("things", "core-thing-5-b", "example-wk-201", "interactive", "ReplicaSet", "core-thing-5", "1Gi"),
		}, ",") + `]}`),
	}, []string{"ledgerkeeper"}, "2026-10-11T04:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func appPod(index int, memory string) pod {
	return pod{Namespace: "ledgerkeeper", Workload: "Deployment/ledgerkeeper", Index: index, Labels: map[string]any{"app": "lk"},
		Spec: capacity.Object{capacity.PriorityClassKey: "interactive", "containers": []any{map[string]any{"name": "c0", "image": "x",
			"resources": map[string]any{"requests": map[string]any{"memory": memory}}}}}}
}

// placed is where each pod of a trial is, by its name, with "?" for one
// left to the scheduler.
func placed(tr trial) map[string]string {
	out := map[string]string{}
	for _, o := range tr.Objects {
		if o["kind"] == capacity.KindPod {
			machine, _ := dig(o, "spec", "nodeName").(string)
			if machine == "" {
				machine = "?"
			}
			out[fmt.Sprint(dig(o, "metadata", "name"))] = machine
		}
	}
	return out
}

// With a worker gone, what ran on it has to be placed again, the machine is
// not there to place it on, and a pod that runs on every machine is not
// made again. Work that can wait is never in a trial.
func TestAWorkerGoneLeavesItsPodsToBePlacedAndItsOwnBehind(t *testing.T) {
	was := asks{Pods: []pod{appPod(0, "3Gi")}}
	all := scenario(north(t), "", was, was)
	if got := placed(all); len(got) != 3 || got["deployment-ledgerkeeper-0"] != "wk-200" {
		t.Errorf("with every machine running, an application's untouched pod is not where it is today, or the trial holds more than what cannot wait: %v", got)
	}
	gone := scenario(north(t), "wk-200", was, was)
	got := placed(gone)
	if len(got) != 2 || got["deployment-ledgerkeeper-0"] != "?" {
		t.Errorf("with wk-200 gone, its pod is not left to be placed, or the pod of every machine was made again: %v", got)
	}
	for _, o := range gone.Objects {
		if o["kind"] == "Node" && dig(o, "metadata", "name") == "wk-200" {
			t.Error("the worker that is gone is still in the trial")
		}
	}
	other := scenario(north(t), "wk-201", was, was)
	moved := 0
	for name, machine := range placed(other) {
		if machine == "?" {
			moved++
			if !strings.Contains(other.Called["x1/"+name]+other.Called["things/"+name]+fmt.Sprint(other.Called), "made by a ReplicaSet, reserving 1Gi") {
				t.Errorf("the pod to be placed is not called what it is: %v", other.Called)
			}
		}
	}
	if moved != 1 {
		t.Errorf("with wk-201 gone, %d pods are to be placed, not the one it ran", moved)
	}
}

// A pod the change alters is the scheduler's to place, and takes the place
// of the one the site runs; one it adds is too; and what a setting outside
// an application was raised by is added as that much more.
func TestWhatAChangeAltersOrAddsIsLeftToTheScheduler(t *testing.T) {
	was := asks{Pods: []pod{appPod(0, "3Gi")}, Core: map[string]workload{
		"raised": {Pods: 1, PodBytes: gib}, "more-of-them": {Pods: 1, PodBytes: gib}, "lowered": {Pods: 2, PodBytes: 2 * gib},
		"waits": {Pods: 1, PodBytes: gib, CanWait: true}}}
	now := asks{Pods: []pod{appPod(0, "5Gi"), appPod(1, "5Gi")}, Core: map[string]workload{
		"raised": {Pods: 1, PodBytes: 3 * gib}, "more-of-them": {Pods: 3, PodBytes: gib}, "lowered": {Pods: 1, PodBytes: gib},
		"waits": {Pods: 9, PodBytes: 9 * gib, CanWait: true}, "new": {Pods: 1, PodBytes: 512 << 20}},
		Claims: []claim{{Namespace: "ledgerkeeper", Name: "books"}, {Namespace: "elsewhere", Name: "fresh",
			Spec: capacity.Object{capacity.StorageClassKey: "kept", "resources": map[string]any{"requests": map[string]any{"storage": "2Gi"}}}}}}
	tr := scenario(north(t), "", was, now)
	got := placed(tr)
	if got["deployment-ledgerkeeper-0"] != "?" || got["deployment-ledgerkeeper-1"] != "?" {
		t.Errorf("an altered pod and an added one were not left to the scheduler: %v", got)
	}
	var stood []string
	for id, called := range tr.Called {
		if strings.HasPrefix(id, more+"/") {
			stood = append(stood, called)
		}
	}
	sort.Strings(stood)
	want := []string{
		"what more-of-them asks for beyond what it did, 1024 MiB", "what more-of-them asks for beyond what it did, 1024 MiB",
		"what new asks for beyond what it did, 512 MiB", "what raised asks for beyond what it did, 2048 MiB",
	}
	if strings.Join(stood, "; ") != strings.Join(want, "; ") {
		t.Errorf("what stands in for the settings that were raised is:\n  %s\nnot:\n  %s", strings.Join(stood, "\n  "), strings.Join(want, "\n  "))
	}
	raw, _ := json.Marshal(tr.Objects)
	for _, held := range []string{`"name":"elsewhere"`, `"name":"engineer-more"`, `"claimRef":{"name":"fresh","namespace":"elsewhere"}`, `"storage":"2Gi"`} {
		if !strings.Contains(string(raw), held) {
			t.Errorf("the trial does not hold %s", held)
		}
	}
	if strings.Count(string(raw), `"kind":"`+capacity.KindVolume+`"`) != 1 {
		t.Errorf("a volume was made for a claim the site already holds, or none for the new one")
	}
	if !same(was, was) || same(was, now) {
		t.Error("two trees were told apart, or not, wrongly")
	}
}

// listing is the pods of a cluster a change is tried in, as kubectl lists
// them.
func listing(pods ...string) func() ([]byte, error) {
	return func() ([]byte, error) { return []byte(`{"items":[` + strings.Join(pods, ",") + `]}`), nil }
}

func listed(name, rest string) string {
	return `{"metadata":{"namespace":"n","name":"` + name + `"` + rest
}

// The scheduler's answer is read off the pods: one with no machine waits
// and says why, one a machine is being cleared for does not, and one put
// off its machine to make a place was evicted, whether it is on its way
// out or already gone.
func TestTheSchedulersAnswerIsReadOffThePods(t *testing.T) {
	loaded := []capacity.Object{{"metadata": map[string]any{"namespace": "n", "name": "gone-already"}}}
	got, err := verdicts(listing(
		listed("on-a-machine", `},"spec":{"nodeName":"wk-1"}}`),
		listed("no-room", `},"status":{"conditions":[{"type":"PodScheduled","message":"0/2 nodes are available: 2 Insufficient memory."}]}}`),
		listed("being-cleared-for", `},"status":{"nominatedNodeName":"wk-1","conditions":[{"type":"PodScheduled","message":"waiting"}]}}`),
		listed("on-its-way-out", `,"deletionTimestamp":"2026-10-11T00:00:00Z"},"spec":{"nodeName":"wk-1"}}`),
	), loaded, time.Millisecond, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || !strings.Contains(got["n/no-room"], "Insufficient memory") || got["n/on-its-way-out"] != evicted || got["n/gone-already"] != evicted {
		t.Errorf("the answer was read as %v", got)
	}
	if _, err := verdicts(listing(listed("unjudged", `}}`)), nil, time.Millisecond, 3); err == nil || !strings.Contains(err.Error(), "no verdict") {
		t.Errorf("a pod the scheduler never spoke of was read as an answer: %v", err)
	}
	if _, err := verdicts(func() ([]byte, error) { return []byte("no"), nil }, nil, time.Millisecond, 1); err == nil {
		t.Error("a list that is not one was read")
	}
	if _, err := verdicts(func() ([]byte, error) { return nil, errors.New("unreachable") }, nil, time.Millisecond, 1); err == nil {
		t.Error("a cluster that did not answer was read as one with nothing waiting")
	}
}

// stubborn is a scheduler that leaves waiting every pod standing in for a
// raised setting, and one pod that waits whatever is asked.
func stubborn(calls *int) judge {
	return func(tr trial) (map[string]string, error) {
		*calls++
		waiting := map[string]string{"things/always": "it waited before the change"}
		for id := range tr.Called {
			if strings.HasPrefix(id, more+"/") {
				waiting[id] = "0/2 nodes are available: 2 Insufficient memory."
			}
		}
		return waiting, nil
	}
}

// record is a site's record with its capacity in it, or with none.
func record(t *testing.T, site *capacity.Snapshot) string {
	t.Helper()
	dir := t.TempDir()
	if site != nil {
		raw, err := capacity.Marshal(*site)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, capacity.File), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func stampOf(t *testing.T, before, after map[string][]string, site *capacity.Snapshot, place judge) (int, string) {
	t.Helper()
	was, now := tree(t, before), tree(t, after)
	var out, complaints bytes.Buffer
	code := stamp([]string{"-site", "north", "-record", record(t, site), "-base", "the-commit-before"}, &out, &complaints, now,
		func(_, revision string) (string, func(), error) {
			if revision != "the-commit-before" {
				t.Errorf("the tree was asked for at %q, not at the commit the change is from", revision)
			}
			return was, func() {}, nil
		}, asDrawn, place)
	return code, out.String() + complaints.String()
}

// A change is refused when the scheduler leaves waiting something that did
// not wait before it, with each worker gone in turn, and says nothing when
// it does not. A change that asks the site for nothing new is not tried.
func TestAChangeIsStampedWhenTheSchedulerPlacesItAndRefusedWhenItDoesNot(t *testing.T) {
	core := "clusters/core/a.yaml"
	site := north(t)
	one := map[string][]string{core: {deployment("thing", "1", "", "1Gi")}}

	calls := 0
	if code, said := stampOf(t, one, one, &site, stubborn(&calls)); code != 0 || said != "" || calls != 0 {
		t.Errorf("a change that asks for nothing new was tried %d time(s), or something was said of it (%d):\n%s", calls, code, said)
	}
	code, said := stampOf(t, one, map[string][]string{core: {deployment("thing", "2", "", "1Gi")}}, &site, stubborn(&calls))
	// With every machine running, and with each of two workers gone, the
	// site before the change and the site with it: six trials.
	if calls != 6 {
		t.Errorf("the change was tried %d times, not before and after for every machine running and each worker gone", calls)
	}
	for _, want := range []string{"REFUSED", "With every machine running", "With wk-200 gone", "With wk-201 gone",
		"what Deployment/things/thing asks for beyond what it did, 1024 MiB: 0/2 nodes are available", "as read 2026-10-11T04:00:00Z"} {
		if code != 1 || !strings.Contains(said, want) {
			t.Errorf("a change the scheduler could not place was not refused saying %q (%d):\n%s", want, code, said)
		}
	}
	if strings.Contains(said, "it waited before the change") {
		t.Errorf("a pod that waited before the change was held against it:\n%s", said)
	}
	placesAll := func(trial) (map[string]string, error) { return nil, nil }
	if code, said := stampOf(t, one, map[string][]string{core: {deployment("thing", "2", "", "1Gi")}}, &site, placesAll); code != 0 || said != "" {
		t.Errorf("a change the scheduler placed was not stamped in silence (%d):\n%s", code, said)
	}
	broken := func(trial) (map[string]string, error) { return nil, errors.New("the cluster would not start") }
	if code, said := stampOf(t, one, map[string][]string{core: {deployment("thing", "2", "", "1Gi")}}, &site, broken); code != 1 || !strings.Contains(said, "would not start") {
		t.Errorf("a change that could not be tried was let through, or not told why (%d):\n%s", code, said)
	}
}

// A site nobody has read cannot be held to anything. A change that asks for
// something is refused for it; one that asks for nothing is not.
func TestAChangeAgainstASiteNobodyHasReadIsRefusedOnlyIfItAsksForSomething(t *testing.T) {
	core := "clusters/core/a.yaml"
	calls := 0
	unchanged := map[string][]string{core: {deployment("thing", "1", "", "1Gi")}}
	if code, said := stampOf(t, unchanged, unchanged, nil, stubborn(&calls)); code != 0 || said != "" {
		t.Errorf("a change that asks for nothing was not let through an unread site in silence (%d):\n%s", code, said)
	}
	code, said := stampOf(t, unchanged, map[string][]string{core: {deployment("thing", "2", "", "1Gi")}}, nil, stubborn(&calls))
	if code != 1 || !strings.Contains(said, "REFUSED") || !strings.Contains(said, "is not on record") || calls != 0 {
		t.Errorf("a change that asks for more was let through a site nobody has read, or not told why (%d):\n%s", code, said)
	}
	// A record written before the snapshot was is no record either: it
	// stops a change that asks for something, and nothing else.
	stale := t.TempDir()
	if err := os.WriteFile(filepath.Join(stale, capacity.File), []byte(`{"workers_gib": 27}`), 0o644); err != nil {
		t.Fatal(err)
	}
	stampStale := func(after map[string][]string) (int, string) {
		was, now := tree(t, unchanged), tree(t, after)
		var out, complaints bytes.Buffer
		code := stamp([]string{"-site", "north", "-record", stale, "-base", "x"}, &out, &complaints, now,
			func(string, string) (string, func(), error) { return was, func() {}, nil }, asDrawn, stubborn(&calls))
		return code, out.String() + complaints.String()
	}
	if code, said := stampStale(unchanged); code != 0 || said != "" {
		t.Errorf("a change that asks for nothing was stopped by a record that cannot be read (%d):\n%s", code, said)
	}
	if code, said := stampStale(map[string][]string{core: {deployment("thing", "2", "", "1Gi")}}); code != 1 || !strings.Contains(said, "the next converge writes one") || calls != 0 {
		t.Errorf("a change that asks for more was let through a record that cannot be read, or not told why (%d):\n%s", code, said)
	}
	var out, complaints bytes.Buffer
	if code := stamp([]string{"-site", "north"}, &out, &complaints, t.TempDir(), nil, asDrawn, nil); code != 2 {
		t.Errorf("a stamp asked for with no record or base answered %d", code)
	}
}

// standInPrograms puts programs of the names the trial runs ahead of the
// real ones. Each writes down what it was asked; the one that lists pods
// answers that everything standing in for a raised setting waits.
func standInPrograms(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "asked.log")
	script := "#!/bin/sh\necho \"$(basename \"$0\") $*\" >> " + log + "\n"
	for _, program := range []string{starter, store, apiServer, scheduler} {
		if err := os.WriteFile(filepath.Join(dir, program), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	lister := script + `case "$*" in
  *--dry-run=server*) for f in "$@"; do last="$f"; done
    if grep -q '"refuse-me"' "$last"; then echo 'Error from server: The PriorityClass "refuse-me" is invalid: preemptionPolicy: Invalid value: "a-value-never-said": field is immutable' >&2; exit 1; fi
    if grep -q '"break-me"' "$last"; then echo 'Unable to connect to the server' >&2; exit 1; fi ;;
  *pods.json*) for f in "$@"; do last="$f"; done; cp "$last" ` + filepath.Join(dir, "pods") + ` ;;
  get*) if grep -q '"namespace":"` + more + `"' ` + filepath.Join(dir, "pods") + ` 2>/dev/null; then
      echo '{"items":[{"metadata":{"namespace":"` + more + `","name":"more-1-0"},"status":{"conditions":[{"type":"PodScheduled","message":"0/2 nodes are available: 2 Insufficient memory."}]}}]}'
    else echo '` + capacity.Nothing + `'; fi ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, asker), []byte(lister), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// quickly has a trial against programs that answer at once not wait on them
// as it would on a real scheduler.
func quickly(t *testing.T) {
	t.Helper()
	was := pause
	pause = time.Millisecond
	t.Cleanup(func() { pause = was })
}

// A trial starts a cluster of three programs and no more, each the one
// already delivered; loads the site before its pods, with each machine
// given its own taints; and stops the cluster when it is done.
func TestATrialStartsLoadsAsksAndStopsInThatOrder(t *testing.T) {
	log := standInPrograms(t)
	quickly(t)
	waiting, err := realScheduler(scenario(north(t), "", asks{}, asks{Core: map[string]workload{"raised": {Pods: 1, PodBytes: gib}}}))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for id, why := range waiting {
		if id == more+"/more-1-0" && strings.Contains(why, "Insufficient memory") {
			found = true
		}
	}
	if !found {
		t.Errorf("what the cluster said waits was read as %v", waiting)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var steps []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		words := strings.Fields(line)
		step := words[0] + " " + words[1]
		if words[1] == "apply" {
			step += " " + filepath.Base(words[len(words)-1])
		}
		if len(steps) == 0 || steps[len(steps)-1] != step {
			steps = append(steps, step)
		}
	}
	want := "kwokctl create, kubectl apply standing.json, kubectl patch, kubectl apply pods.json, kubectl get, kwokctl delete"
	if strings.Join(steps, ", ") != want {
		t.Errorf("the trial did:\n  %s\nnot:\n  %s", strings.Join(steps, ", "), want)
	}
	first := strings.Split(string(raw), "\n")[0]
	for _, held := range []string{"--components etcd,kube-apiserver,kube-scheduler", "--etcd-binary ", "--kube-apiserver-binary ", "--kube-scheduler-binary ", "--runtime binary"} {
		if !strings.Contains(first, held) {
			t.Errorf("the cluster was started without %q: %s", held, first)
		}
	}
	if strings.Count(string(raw), "kubectl patch node") != 3 || !strings.Contains(string(raw), `{"spec":{"taints":null}}`) {
		t.Errorf("each of three machines was not given its own taints and no others:\n%s", raw)
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
	got, err := asked(dir, "north", 3, asDrawn)
	if err != nil {
		t.Fatal(err)
	}
	if got.Core["Deployment/things/thing"].Pods != 1 {
		t.Errorf("the commit's tree was read as the working tree: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs")); err == nil {
		t.Error("more than a site's work was taken out of the commit")
	}
	if _, _, err := archived(repo, "no-such-commit"); err == nil {
		t.Error("a commit that does not exist gave a tree")
	}
}

// The program itself, started in a checkout, judges that checkout: it takes
// the commit before out of it, has the real drafter redraw both, and leaves
// quietly when the change asks for nothing new. This is the one test here that reads YAML,
// because it is the one that goes through the drafter.
func TestTheProgramItselfJudgesTheCheckoutItIsStartedIn(t *testing.T) {
	repo := t.TempDir()
	manifest := filepath.Join(repo, "clusters", "core", "a.yaml")
	write := func(replicas string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
			t.Fatal(err)
		}
		body := "kind: Deployment\nmetadata:\n  name: thing\n  namespace: things\nspec:\n  replicas: " + replicas +
			"\n  template:\n    spec:\n      containers:\n        - name: c\n          resources:\n            requests:\n              memory: 1Gi\n"
		if err := os.WriteFile(manifest, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("1")
	// What marks the top of a checkout of this repository, for a program
	// that finds its repository from where it is started.
	if err := os.WriteFile(filepath.Join(repo, "CLAUDE.md"), []byte("a checkout\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	t.Chdir(repo)

	// Asked something it does not do.
	var out, complaints bytes.Buffer
	if code := run([]string{"approve"}, &out, &complaints); code != 2 || !strings.Contains(complaints.String(), "usage: engineer") {
		t.Errorf("asked something it does not do, the program answered %d: %s", code, complaints.String())
	}

	// Another of a gibibyte, tried in a cluster that leaves it waiting.
	standInPrograms(t)
	quickly(t)
	write("2")
	site := north(t)
	held := record(t, &site)
	out.Reset()
	if code := run([]string{"stamp", "-site", "north", "-record", held, "-base", "HEAD"}, &out, &complaints); code != 1 || !strings.Contains(out.String(), "REFUSED") {
		t.Errorf("a change that does not fit was not refused by the program itself (%d): %s%s", code, out.String(), complaints.String())
	}

	// And as it is started: on its own arguments, leaving quietly for a
	// change that asks the site for nothing new.
	write("1")
	args := os.Args
	t.Cleanup(func() { os.Args = args })
	os.Args = []string{args[0], "stamp", "-site", "north", "-record", held, "-base", "HEAD"}
	main()
}

// A cluster that will not start is an error that says what the program
// starting it said last, and never a site with nothing waiting.
func TestAClusterThatWillNotStartIsNotAnAnswer(t *testing.T) {
	dir := t.TempDir()
	refuses := "#!/bin/sh\necho 'downloading' >&2\necho 'no such release' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, starter), []byte(refuses), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	waiting, err := realScheduler(scenario(north(t), "", asks{}, asks{}))
	if err == nil || waiting != nil || !strings.Contains(err.Error(), "could not be started") || !strings.Contains(err.Error(), "no such release") {
		t.Errorf("a cluster that would not start answered %v, %v", waiting, err)
	}
}

// A record outlives the program that took it. The first taken of a real
// site held two things a cluster refuses: a namespace labelled for how its
// pods are secured, under a value that had been replaced, and a pod whose
// first container and first init container had one name between them. A
// trial loads neither as it was written.
func TestATrialLoadsARecordTakenBeforeTheSnapshotWasPutRight(t *testing.T) {
	old := capacity.Snapshot{Objects: []capacity.Object{
		{"kind": "Namespace", "metadata": map[string]any{"name": "x1", "labels": map[string]any{
			"pod-security.kubernetes.io/enforce": "x2", "selected-by": "x1"}}},
		{"kind": "Node", "metadata": map[string]any{"name": "wk-200"}},
		{"kind": capacity.KindPod, "metadata": map[string]any{"namespace": "x1", "name": "x3"}, "spec": map[string]any{"nodeName": "wk-200",
			capacity.InitContainersKey: []any{map[string]any{"name": "c0"}},
			"containers":               []any{map[string]any{"name": "c0"}, map[string]any{"name": "c1"}}}},
	}}
	raw, err := json.Marshal(scenario(old, "", asks{}, asks{Core: map[string]workload{"raised": {Pods: 1, PodBytes: gib}}}).Objects)
	if err != nil {
		t.Fatal(err)
	}
	loaded := string(raw)
	if strings.Contains(loaded, "pod-security") || !strings.Contains(loaded, `"selected-by":"x1"`) {
		t.Errorf("the namespace is loaded with a label it is refused for, or without the one it is selected by: %s", loaded)
	}
	if !strings.Contains(loaded, `"initContainers":[{"name":"i0"}]`) || !strings.Contains(loaded, `"containers":[{"name":"c0"},{"name":"c1"}]`) {
		t.Errorf("the pod's containers do not each have a name of their own: %s", loaded)
	}
	// What stands in for a raised setting goes where the estate puts work.
	if !strings.Contains(loaded, `"key":"`+capacity.ControlPlaneLabel+`","operator":"`+capacity.HasNoLabel+`"`) {
		t.Errorf("a pod standing in for a raised setting may be placed on a control plane: %s", loaded)
	}
}

// A runner made for one job is how busy the site was when it was recorded,
// and is never in a trial: one the record caught queued behind another is
// not a pod with nowhere to go. Everything beside it is as it was.
func TestARunnerMadeForOneJobIsNotHeldToTheSiteAsItWasRecorded(t *testing.T) {
	s, err := capacity.Take(capacity.Listings{
		Nodes:           []byte(`{"items":[{"metadata":{"name":"example-wk-200","labels":{}},"status":{"allocatable":{"memory":"2Gi"}}}]}`),
		Namespaces:      []byte(`{"items":[{"metadata":{"name":"things"}}]}`),
		PriorityClasses: []byte(capacity.Nothing), StorageClasses: []byte(capacity.Nothing), Volumes: []byte(capacity.Nothing), Claims: []byte(capacity.Nothing),
		Pods: []byte(`{"items":[
 {"metadata":{"namespace":"things","name":"busy-runner-a","ownerReferences":[{"kind":"` + capacity.OneJob + `","name":"busy-runner-a"}]},
  "spec":{"nodeName":"example-wk-200","priorityClassName":"interactive","containers":[{"name":"c","resources":{"requests":{"memory":"1Gi"}}}]},"status":{"phase":"Running"}},
 {"metadata":{"namespace":"things","name":"busy-runner-b","ownerReferences":[{"kind":"` + capacity.OneJob + `","name":"busy-runner-b"}]},
  "spec":{"priorityClassName":"interactive","containers":[{"name":"c","resources":{"requests":{"memory":"1Gi"}}}]},"status":{"phase":"Pending"}},
 {"metadata":{"namespace":"things","name":"core-thing-5-b","ownerReferences":[{"kind":"ReplicaSet","name":"core-thing-5"}]},
  "spec":{"nodeName":"example-wk-200","priorityClassName":"interactive","containers":[{"name":"c","resources":{"requests":{"memory":"512Mi"}}}]},"status":{"phase":"Running"}}]}`),
	}, nil, "2026-10-11T04:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	got := placed(scenario(s, "", asks{}, asks{}))
	if len(got) != 1 {
		t.Errorf("the trial holds %v: a runner made for one job was held to the site, or what the site runs was not", got)
	}
	for _, called := range scenario(s, "", asks{}, asks{}).Called {
		if strings.Contains(called, capacity.OneJob) {
			t.Errorf("a runner made for one job is in the trial as %q", called)
		}
	}
}

// What is tried instead is a runner for each place runners are sent: the
// largest any set sends there, once, because sets sent to the same machines
// queue behind each other. A set sent somewhere else is a place of its own.
// A runner is tried as a pod, and is not counted again among what the core
// reserves; and a set that says nothing of its runners is not guessed at.
func TestARunnerIsTriedForEachPlaceRunnersAreSent(t *testing.T) {
	set := func(name, memory, nodeSelector string) string {
		spec := `"containers":[{"name":"runner","resources":{"requests":{"memory":"` + memory + `"}}}],"priorityClassName":"interactive"`
		if nodeSelector != "" {
			spec += `,"nodeSelector":{"kept":"` + nodeSelector + `"}`
		}
		return `{"kind":"HelmRelease","metadata":{"name":"` + name + `","namespace":"runners"},"spec":{"chart":{"spec":{"chart":"` + runnerChart + `"}},
		  "values":{"maxRunners":3,"template":{"metadata":{"labels":{"set":"` + name + `"}},"spec":{` + spec + `}}}}}`
	}
	dir := tree(t, map[string][]string{
		"clusters/core/sets.yaml": {
			set("small", "1Gi", "away"), set("large", "2Gi", "away"), set("trusted", "512Mi", ""),
			`{"kind":"HelmRelease","metadata":{"name":"other","namespace":"things"},"spec":{"chart":{"spec":{"chart":"other"}},"values":{"resources":{"requests":{"memory":"64Mi"}}}}}`,
		},
	})
	got, err := asked(dir, "north", 3, asDrawn)
	if err != nil {
		t.Fatal(err)
	}
	tried := map[string]string{}
	for _, p := range got.Pods {
		w, err := podOf(capacity.Object{"template": capacity.Object{"spec": p.Spec}})
		if err != nil {
			t.Fatal(err)
		}
		tried[p.Workload] = fmt.Sprint(w.PodBytes)
		if p.Namespace != "runners" {
			t.Errorf("%s is tried in %q, not the namespace its set is in", p.Workload, p.Namespace)
		}
	}
	want := map[string]string{"HelmRelease/large": fmt.Sprint(2 * gib), "HelmRelease/trusted": fmt.Sprint(gib / 2)}
	if fmt.Sprint(tried) != fmt.Sprint(want) {
		t.Errorf("the runners tried are %v, want the largest for each place: %v", tried, want)
	}
	for at := range got.Core {
		if strings.Contains(at, "template") {
			t.Errorf("a set's runner is counted among what the core reserves as well (%s)", at)
		}
	}
	if _, counted := got.Core["HelmRelease/things/other"]; !counted {
		t.Errorf("a release that is not a set of runners is no longer counted: %v", got.Core)
	}

	blind := tree(t, map[string][]string{
		"clusters/core/sets.yaml": {`{"kind":"HelmRelease","metadata":{"name":"blind","namespace":"runners"},"spec":{"chart":{"spec":{"chart":"` + runnerChart + `"}},"values":{}}}`},
	})
	if _, err := asked(blind, "north", 3, asDrawn); err == nil || !strings.Contains(err.Error(), "blind") {
		t.Errorf("a set of runners that says nothing of its runners was guessed at: %v", err)
	}
}

// classDoc is a PriorityClass as a drawing, with Flux's marker or without.
func classDoc(name, policy string, forced bool) string {
	meta := `"name":"` + name + `"`
	if forced {
		meta += `,"annotations":{"` + flux.Force + `":"` + flux.Forced + `"}`
	}
	return `{"apiVersion":"scheduling.k8s.io/v1","kind":"PriorityClass","metadata":{` + meta + `},"value":10000,"preemptionPolicy":"` + policy + `"}`
}

// An edit is a class the change alters or adds, and not one it leaves as it
// was, nor one Flux replaces rather than edits.
func TestAnEditIsAClassTheChangeAltersThatFluxWouldEdit(t *testing.T) {
	before := tree(t, map[string][]string{"clusters/core/classes.yaml": {
		classDoc("kept", "Never", false), classDoc("altered", "Never", false), classDoc("replaced", "Never", true)}})
	after := tree(t, map[string][]string{"clusters/core/classes.yaml": {
		classDoc("kept", "Never", false), classDoc("altered", capacity.PreemptsLower, false), classDoc("replaced", capacity.PreemptsLower, true), classDoc("added", "Never", false)}})
	was, err := asked(before, "north", 3, asDrawn)
	if err != nil {
		t.Fatal(err)
	}
	is, err := asked(after, "north", 3, asDrawn)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range edited(was, is) {
		names = append(names, fmt.Sprint(dig(e, "metadata", "name")))
	}
	if strings.Join(names, ",") != "added,altered" {
		t.Errorf("the edits were read as %v, not the class added and the one altered", names)
	}
}

// What the cluster refuses is said as the object, the field and the rule,
// and never the value; what fails for any other reason is an error, not a
// refusal of the change; and the site's own objects go in first.
func TestAnEditTheClusterRefusesIsSaidAndAnythingElseIsAnError(t *testing.T) {
	log := standInPrograms(t)
	var edits []capacity.Object
	for _, doc := range []string{classDoc("taken", "Never", false), classDoc("refuse-me", capacity.PreemptsLower, false)} {
		var o capacity.Object
		if err := json.Unmarshal([]byte(doc), &o); err != nil {
			t.Fatal(err)
		}
		edits = append(edits, o)
	}
	refused, err := refusedEdits(nil, edits)
	if err != nil {
		t.Fatal(err)
	}
	if len(refused) != 1 || strings.TrimSpace(refused[0]) != "PriorityClass/refuse-me: preemptionPolicy: field is immutable" {
		t.Errorf("what was refused was said as %q", refused)
	}
	if strings.Contains(strings.Join(refused, ""), "a-value-never-said") {
		t.Error("the value the refusal quoted was said")
	}
	raw, _ := os.ReadFile(log)
	if !strings.Contains(string(raw), "kept.json") || strings.Index(string(raw), "kept.json") > strings.Index(string(raw), "edit-0.json") {
		t.Errorf("the site's own objects were not loaded before the edits were tried:\n%s", raw)
	}

	var broken capacity.Object
	_ = json.Unmarshal([]byte(classDoc("break-me", "Never", false)), &broken)
	if _, err := refusedEdits(nil, []capacity.Object{broken}); err == nil {
		t.Error("a cluster that could not be reached was read as the change being taken")
	}
}

// The plan refuses a change the cluster would refuse as Flux applies it,
// before it asks whether there is room, and says what and why.
func TestAChangeTheClusterWouldRefuseIsRefused(t *testing.T) {
	standInPrograms(t)
	quickly(t)
	site := north(t)
	code, said := stampOf(t,
		map[string][]string{"clusters/core/classes.yaml": {classDoc("refuse-me", "Never", false)}},
		map[string][]string{"clusters/core/classes.yaml": {classDoc("refuse-me", capacity.PreemptsLower, false)}},
		&site, func(trial) (map[string]string, error) {
			t.Error("the scheduler was asked about a change the cluster refuses outright")
			return nil, nil
		})
	if code != 1 || !strings.Contains(said, "would refuse this change as Flux applies it") || !strings.Contains(said, "PriorityClass/refuse-me: preemptionPolicy: field is immutable") {
		t.Errorf("a change the cluster refuses was not refused, or not said (%d):\n%s", code, said)
	}
}
