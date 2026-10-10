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

// asks is what the workload asks the workers to hold for it. Work that can
// wait asks for nothing: it runs in whatever is free and is not counted.
func (w workload) asks() int64 {
	if w.CanWait {
		return 0
	}
	return int64(w.Pods) * w.PodBytes
}

// drawer has the drafter redraw paths, and directories to be built, of one
// tree. What comes back is each file by its path and each built directory
// by drafter.Built and its path, as JSON.
type drawer func(tree string, builds []string, paths ...string) (map[string]json.RawMessage, error)

// reserved is everything a tree declares for one site that reserves memory,
// by a name that is the same in two trees when it is the same thing.
//
// Three places are read, which are the three ways a site is given work: the
// core every site runs, what the site's own directory adds, and each
// application the site was given, built with the environment's settings it
// was given it in. workers is how many machines a pod-for-each-machine
// workload lands on.
//
// Nothing here reads a manifest's own format. The drafter redraws each one,
// and this reads the drawing.
func reserved(tree, site string, workers int, draw drawer) (map[string]workload, error) {
	out := map[string]workload{}
	var paths []string
	for _, dir := range []string{applications.SitesDir + "/core", applications.SitesDir + "/" + site} {
		if _, err := os.Stat(filepath.Join(tree, filepath.FromSlash(dir))); err == nil {
			paths = append(paths, dir)
		}
	}
	if len(paths) == 0 {
		return out, nil
	}
	drawn, err := draw(tree, nil, paths...)
	if err != nil {
		return nil, err
	}
	var builds []string
	for _, rel := range sortedKeys(drawn) {
		if !isManifest(rel) {
			continue
		}
		docs, err := documents(rel, drawn[rel])
		if err != nil {
			return nil, err
		}
		if err := declared(rel, docs, workers, out); err != nil {
			return nil, err
		}
		if strings.HasPrefix(rel, applications.SitesDir+"/"+site+"/") {
			builds = append(builds, given(docs)...)
		}
	}
	if len(builds) == 0 {
		return out, nil
	}
	sort.Strings(builds)
	for _, dir := range builds {
		if _, err := os.Stat(filepath.Join(tree, filepath.FromSlash(dir))); err != nil {
			return nil, fmt.Errorf("the site is given what %s holds, and it is not there, so what it reserves is not known", dir)
		}
	}
	assembled, err := draw(tree, builds)
	if err != nil {
		return nil, fmt.Errorf("an application the site is given could not be built, so what it reserves is not known: %w", err)
	}
	for _, dir := range builds {
		docs, err := documents(dir, assembled[drafter.Built+dir])
		if err != nil {
			return nil, err
		}
		if err := declared("what "+dir+" builds to", docs, workers, out); err != nil {
			return nil, err
		}
	}
	return out, nil
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

// given is the directories a site's own documents tell Flux to build from
// an application: each is an application the site was given, with the
// environment's settings it was given it in.
func given(docs []map[string]any) []string {
	var out []string
	for _, doc := range docs {
		if doc["kind"] != flux.Kustomization {
			continue
		}
		spec, _ := doc["spec"].(map[string]any)
		path, _ := spec["path"].(string)
		if dir := strings.TrimPrefix(path, "./"); strings.HasPrefix(dir, applications.Dir+"/") {
			out = append(out, dir)
		}
	}
	return out
}

// declared adds what each document reserves.
func declared(from string, docs []map[string]any, workers int, out map[string]workload) error {
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
			if err := inValues(name, values, false, out); err != nil {
				return fmt.Errorf("%s in %s: %w", name, from, err)
			}
		}
	}
	return nil
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

// change is what a tree asks for that another did not: what was added to
// what work that cannot wait reserves, what was given back, and the largest
// single pod among what is new or was altered.
func change(before, after map[string]workload) (capacity.Change, []string) {
	var c capacity.Change
	var grew []string
	for name, now := range after {
		was := before[name]
		switch d := now.asks() - was.asks(); {
		case d > 0:
			c.Added += d
			grew = append(grew, name)
		case d < 0:
			c.Removed += -d
		}
		if now != was && !now.CanWait && now.PodBytes > c.LargestPod {
			c.LargestPod = now.PodBytes
		}
	}
	for name, was := range before {
		if _, still := after[name]; !still {
			c.Removed += was.asks()
		}
	}
	sort.Strings(grew)
	return c, grew
}
