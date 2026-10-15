package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"homelab/details/applications"
	"homelab/details/capacity"
	"homelab/details/drafter"
	"homelab/details/flux"
)

// workload is one thing a tree declares that reserves memory: how many pods
// it asks for, what each reserves, and whether it is work that can wait.
type workload struct {
	Pods     int
	PodBytes int64
	CanWait  bool
}

// pod is one pod of an application, as its manifest makes it: the workload
// it belongs to, which of that workload's pods it is, and everything of it
// a scheduler reads.
type pod struct {
	Namespace, Workload string
	Index               int
	Labels              any
	Spec                capacity.Object
}

// claim is a volume an application's manifests ask for.
type claim struct {
	Namespace, Name string
	Spec            capacity.Object
}

// asks is what a tree asks a site to run. An application's pods are known
// exactly, since its manifests are the pods. Everything else is known only
// by what it reserves: which pod of a chart a setting belongs to is the
// chart's to say, and a chart is not rendered here.
type asks struct {
	Core   map[string]workload
	Pods   []pod
	Claims []claim
	// Each is the workloads among Core that run a pod on every machine,
	// with everything of that pod a scheduler reads. What one of these is
	// raised by is taken from every machine, and is tried on each.
	Each map[string]capacity.Object
	// Classes is each PriorityClass the tree declares, by name, as it is
	// written: what an edit of the site's own classes would apply.
	Classes map[string]capacity.Object
}

// drawer has the drafter redraw paths, and directories to be built, of one
// tree. What comes back is each file by its path and each built directory
// by drafter.Built and its path, as JSON.
type drawer func(tree string, builds []string, paths ...string) (map[string]json.RawMessage, error)

// asked is everything a tree declares for one site that reserves memory.
//
// Three places are read, which are the three ways a site is given work: the
// core every site runs, what the site's own directory adds, and each
// application the site was given, built with the environment's settings it
// was given it in. workers is how many machines a pod-for-each-machine
// workload lands on.
//
// Nothing here reads a manifest's own format. The drafter redraws each one,
// and this reads the drawing.
func asked(tree, site string, workers int, draw drawer) (asks, error) {
	out := asks{Core: map[string]workload{}, Each: map[string]capacity.Object{}, Classes: map[string]capacity.Object{}}
	var paths []string
	// The network is the third: it is what every machine runs before Flux
	// can, applied by a converge and not by Flux, and it reserves on every
	// machine like anything else that runs there.
	for _, dir := range []string{applications.SitesDir + "/core", applications.SitesDir + "/bootstrap", applications.SitesDir + "/" + site} {
		if _, err := os.Stat(filepath.Join(tree, filepath.FromSlash(dir))); err == nil {
			paths = append(paths, dir)
		}
	}
	if len(paths) == 0 {
		return out, nil
	}
	drawn, err := draw(tree, nil, paths...)
	if err != nil {
		return out, err
	}
	pools := map[string]pod{}
	var builds []build
	for _, rel := range sortedKeys(drawn) {
		if !isManifest(rel) {
			continue
		}
		docs, err := documents(rel, drawn[rel])
		if err != nil {
			return out, err
		}
		if err := declared(rel, docs, workers, out.Core, out.Each); err != nil {
			return out, err
		}
		if err := runners(rel, docs, pools); err != nil {
			return out, err
		}
		for _, doc := range docs {
			if doc["kind"] == capacity.KindPriorityClass {
				meta, _ := doc["metadata"].(map[string]any)
				out.Classes[fmt.Sprint(meta["name"])] = doc
			}
		}
		if strings.HasPrefix(rel, applications.SitesDir+"/"+site+"/") {
			builds = append(builds, given(docs)...)
		}
	}
	for _, where := range sortedKeysOf(pools) {
		out.Pods = append(out.Pods, pools[where])
	}
	if len(builds) == 0 {
		return out, nil
	}
	sort.Slice(builds, func(i, j int) bool { return builds[i].Dir < builds[j].Dir })
	var dirs []string
	for _, b := range builds {
		if _, err := os.Stat(filepath.Join(tree, filepath.FromSlash(b.Dir))); err != nil {
			return out, fmt.Errorf("the site is given what %s holds, and it is not there, so what it reserves is not known", b.Dir)
		}
		dirs = append(dirs, b.Dir)
	}
	assembled, err := draw(tree, dirs)
	if err != nil {
		return out, fmt.Errorf("an application the site is given could not be built, so what it reserves is not known: %w", err)
	}
	for _, b := range builds {
		docs, err := documents(b.Dir, assembled[drafter.Built+b.Dir])
		if err != nil {
			return out, err
		}
		if err := out.application("what "+b.Dir+" builds to", b.Namespace, docs, workers); err != nil {
			return out, err
		}
	}
	return out, nil
}

// application adds what an application's built documents ask for. The
// workloads whose manifests are their pods are taken as those pods; the few
// kinds that are not are counted by what they reserve, as the core is.
func (a *asks) application(from, namespace string, docs []map[string]any, workers int) error {
	same := func(s string) string { return s }
	var others []map[string]any
	for _, doc := range docs {
		kind, _ := doc["kind"].(string)
		meta, _ := doc["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		spec, _ := doc["spec"].(map[string]any)
		switch kind {
		case capacity.KindClaim:
			a.Claims = append(a.Claims, claim{Namespace: namespace, Name: name, Spec: spec})
		case "Deployment", "StatefulSet":
			if w, err := podOf(spec); err != nil {
				return fmt.Errorf("%s %s in %s: %w", kind, name, from, err)
			} else if w.CanWait {
				continue
			}
			template, _ := spec["template"].(map[string]any)
			made, _ := template["spec"].(map[string]any)
			labels, _ := template["metadata"].(map[string]any)
			templates, _ := spec[capacity.ClaimTemplatesKey].([]any)
			for i := 0; i < count(spec["replicas"]); i++ {
				scheduled := capacity.Scheduling(made, same, same, same)
				// A StatefulSet gives each of its pods a claim of its own,
				// named for the template, the set and the pod's number.
				for _, t := range templates {
					tpl, _ := t.(map[string]any)
					tplMeta, _ := tpl["metadata"].(map[string]any)
					tplSpec, _ := tpl["spec"].(map[string]any)
					claimed := fmt.Sprintf("%v-%s-%d", tplMeta["name"], name, i)
					a.Claims = append(a.Claims, claim{Namespace: namespace, Name: claimed, Spec: tplSpec})
					volumes, _ := scheduled["volumes"].([]any)
					scheduled["volumes"] = append(volumes, capacity.Object{"name": fmt.Sprint("t", len(volumes)),
						capacity.ClaimKey: capacity.Object{"claimName": claimed}})
				}
				a.Pods = append(a.Pods, pod{Namespace: namespace, Workload: kind + "/" + name, Index: i, Labels: labels["labels"], Spec: scheduled})
			}
		default:
			others = append(others, doc)
		}
	}
	return declared(from, others, workers, a.Core, a.Each)
}

func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// isManifest says a drawing is of a file of manifests: the drafter redraws
// other things a directory may hold, and those declare no workload.
func isManifest(rel string) bool {
	return strings.HasSuffix(rel, ".yaml") || strings.HasSuffix(rel, ".yml")
}

// documents is a drawing as its list of documents. Numbers are kept as they
// were written, since a size is read from its own text.
func documents(of string, drawing json.RawMessage) ([]map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(drawing))
	dec.UseNumber()
	var docs []map[string]any
	if err := dec.Decode(&docs); err != nil {
		return nil, fmt.Errorf("the drawing of %s is not a list of documents: %w", of, err)
	}
	return docs, nil
}

// build is an application a site was given: the directory Flux builds it
// from, with the environment's settings it was given it in, and the
// namespace it is put in.
type build struct{ Dir, Namespace string }

// given is the applications a site's own documents tell Flux to build.
func given(docs []map[string]any) []build {
	var out []build
	for _, doc := range docs {
		if doc["kind"] != flux.Kustomization {
			continue
		}
		spec, _ := doc["spec"].(map[string]any)
		path, _ := spec["path"].(string)
		if dir := strings.TrimPrefix(path, "./"); strings.HasPrefix(dir, applications.Dir+"/") {
			namespace, _ := spec["targetNamespace"].(string)
			out = append(out, build{Dir: dir, Namespace: namespace})
		}
	}
	return out
}

// declared adds what each document reserves.
func declared(from string, docs []map[string]any, workers int, out map[string]workload, each map[string]capacity.Object) error {
	for _, doc := range docs {
		kind, _ := doc["kind"].(string)
		meta, _ := doc["metadata"].(map[string]any)
		name := fmt.Sprintf("%s/%v/%v", kind, meta["namespace"], meta["name"])
		spec, _ := doc["spec"].(map[string]any)
		switch kind {
		case "Deployment", "StatefulSet":
			w, err := podOf(spec)
			if err != nil {
				return fmt.Errorf("%s in %s: %w", name, from, err)
			}
			w.Pods = count(spec["replicas"])
			out[name] = w
		case "DaemonSet":
			w, err := podOf(spec)
			if err != nil {
				return fmt.Errorf("%s in %s: %w", name, from, err)
			}
			w.Pods = workers
			out[name] = w
			// Kept with where its pods may go, so that what it is raised
			// by can be tried on each machine it runs on and no other.
			template, _ := spec["template"].(map[string]any)
			made, _ := template["spec"].(map[string]any)
			same := func(s string) string { return s }
			each[name] = capacity.Scheduling(made, same, same, same)
		case "Job":
			w, err := podOf(spec)
			if err != nil {
				return fmt.Errorf("%s in %s: %w", name, from, err)
			}
			w.Pods = 1
			out[name] = w
		case "CronJob":
			job, _ := spec["jobTemplate"].(map[string]any)
			inner, _ := job["spec"].(map[string]any)
			w, err := podOf(inner)
			if err != nil {
				return fmt.Errorf("%s in %s: %w", name, from, err)
			}
			w.Pods = 1
			out[name] = w
		case flux.HelmRelease:
			values, _ := spec["values"].(map[string]any)
			// A set of runners' runner is tried as a pod of its own
			// (runners), and is not counted here as well.
			if dig(spec, "chart", "spec", "chart") == runnerChart {
				trimmed := map[string]any{}
				for k, v := range values {
					if k != "template" {
						trimmed[k] = v
					}
				}
				values = trimmed
			}
			if err := inValues(name, values, false, out); err != nil {
				return fmt.Errorf("%s in %s: %w", name, from, err)
			}
		}
	}
	return nil
}

// runnerChart is the chart a set of runners is installed with.
const runnerChart = "gha-runner-scale-set"

// runners adds a runner for each place runners are sent.
//
// A runner is made for a job and is gone with it, so the site's record holds
// however many happened to be running, and the trial leaves those out. What
// has to be true instead is that a job always has somewhere to start: for
// each set of machines runners may be sent to, the largest runner any set
// sends there, once. Two sets sent to the same machines share that one,
// because their jobs queue behind each other, which is a wait and not a
// lack of room.
func runners(from string, docs []map[string]any, pools map[string]pod) error {
	same := func(s string) string { return s }
	for _, doc := range docs {
		if doc["kind"] != flux.HelmRelease {
			continue
		}
		spec, _ := doc["spec"].(map[string]any)
		if dig(spec, "chart", "spec", "chart") != runnerChart {
			continue
		}
		meta, _ := doc["metadata"].(map[string]any)
		values, _ := spec["values"].(map[string]any)
		made, _ := dig(values, "template", "spec").(map[string]any)
		if made == nil {
			return fmt.Errorf("the set of runners %v in %s says nothing of its runners, so where they go and what they reserve is not known", meta["name"], from)
		}
		w, err := podOf(capacity.Object{"template": capacity.Object{"spec": made}})
		if err != nil {
			return fmt.Errorf("the set of runners %v in %s: %w", meta["name"], from, err)
		}
		scheduled := capacity.Scheduling(made, same, same, same)
		place := capacity.Object{}
		for k, v := range scheduled {
			if k != "containers" && k != capacity.InitContainersKey {
				place[k] = v
			}
		}
		key, err := json.Marshal(place)
		if err != nil {
			return err
		}
		if have, ok := pools[string(key)]; ok {
			if had, _ := podOf(capacity.Object{"template": capacity.Object{"spec": have.Spec}}); had.PodBytes >= w.PodBytes {
				continue
			}
		}
		labels, _ := dig(values, "template", "metadata").(map[string]any)
		namespace, _ := meta["namespace"].(string)
		pools[string(key)] = pod{Namespace: namespace, Workload: fmt.Sprintf("%s/%v", flux.HelmRelease, meta["name"]), Labels: labels["labels"], Spec: scheduled}
	}
	return nil
}

func sortedKeysOf(m map[string]pod) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// podOf is one pod of a workload: what its containers reserve between them,
// and whether it is work that can wait.
func podOf(spec map[string]any) (workload, error) {
	template, _ := spec["template"].(map[string]any)
	pod, _ := template["spec"].(map[string]any)
	var w workload
	w.CanWait = pod[capacity.PriorityClassKey] == capacity.CanWait
	containers, _ := pod["containers"].([]any)
	for _, c := range containers {
		container, _ := c.(map[string]any)
		n, err := requested(container["resources"])
		if err != nil {
			return w, fmt.Errorf("container %v: %w", container["name"], err)
		}
		w.PodBytes += n
	}
	return w, nil
}

// requested is the memory one `resources` asks to have reserved, and nothing
// for one that asks for none.
func requested(resources any) (int64, error) {
	r, _ := resources.(map[string]any)
	requests, _ := r["requests"].(map[string]any)
	written, asks := requests["memory"]
	if !asks {
		return 0, nil
	}
	return capacity.Bytes(fmt.Sprint(written))
}

// count is a number of pods as a manifest writes it. One where it says
// nothing, or says something that is not a number, which is the cluster's
// own default.
func count(written any) int {
	if n, ok := written.(json.Number); ok {
		if i, err := n.Int64(); err == nil && i >= 0 {
			return int(i)
		}
	}
	return 1
}

// inValues finds what a chart's settings reserve.
//
// A chart puts its pods' reservations wherever its author chose, so the
// settings are walked and every `resources` that asks for memory is taken,
// under the path it was found at. How many pods share it is the `replicas`
// or `replicaCount` beside it, where there is one. This cannot see a pod
// the settings do not mention, and it is not asked to: what a site really
// runs is read from the site, and this is only for what a change adds.
func inValues(at string, values map[string]any, waits bool, out map[string]workload) error {
	if values[capacity.PriorityClassKey] == capacity.CanWait {
		waits = true
	}
	if n, err := requested(values["resources"]); err != nil {
		return fmt.Errorf("at %s: %w", at, err)
	} else if n > 0 {
		pods := 1
		for _, key := range []string{"replicas", "replicaCount"} {
			if written, said := values[key]; said {
				pods = count(written)
			}
		}
		out[at] = workload{Pods: pods, PodBytes: n, CanWait: waits}
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch v := values[k].(type) {
		case map[string]any:
			if err := inValues(at+"."+k, v, waits, out); err != nil {
				return err
			}
		case []any:
			for i, item := range v {
				if m, ok := item.(map[string]any); ok {
					if err := inValues(fmt.Sprintf("%s.%s[%d]", at, k, i), m, waits, out); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
