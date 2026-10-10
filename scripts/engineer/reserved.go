package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"homelab/details/applications"
	"homelab/details/capacity"
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

// renderer builds one directory the way the cluster will: an application's
// settings are assembled from its base, and only the assembled manifests
// say what it reserves.
type renderer func(dir string) ([]byte, error)

// reserved is everything a tree declares for one site that reserves memory,
// by a name that is the same in two trees when it is the same thing.
//
// Three places are read, which are the three ways a site is given work: the
// core every site runs, what the site's own directory adds, and each
// application the site was given, built with the environment's settings it
// was given it in. workers is how many machines a pod-for-each-machine
// workload lands on.
func reserved(tree, site string, workers int, render renderer) (map[string]workload, error) {
	out := map[string]workload{}
	for _, dir := range []string{filepath.Join(applications.SitesDir, "core"), filepath.Join(applications.SitesDir, site)} {
		err := filepath.WalkDir(filepath.Join(tree, dir), func(path string, d fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if err != nil || d.IsDir() || !isYAML(path) {
				return err
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(tree, path)
			return declared(filepath.ToSlash(rel), body, workers, out)
		})
		if err != nil {
			return nil, err
		}
	}
	assigned, err := applications.Assigned(tree, site)
	if err != nil {
		return nil, err
	}
	for _, a := range assigned {
		dir := filepath.Join(tree, applications.Dir, a.Application, a.Environment)
		body, err := render(dir)
		if err != nil {
			return nil, fmt.Errorf("the application %s could not be built with its %s settings, so what it reserves is not known: %w", a.Application, a.Environment, err)
		}
		if err := declared("application "+a.Application, body, workers, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func isYAML(path string) bool {
	return strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml")
}

// declared reads every document in one file and adds what reserves.
func declared(from string, body []byte, workers int, out map[string]workload) error {
	dec := yaml.NewDecoder(bytes.NewReader(body))
	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s is not YAML that can be read, so what it reserves is not known: %w", from, err)
		}
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
		case "HelmRelease":
			values, _ := spec["values"].(map[string]any)
			if err := inValues(name, values, false, out); err != nil {
				return fmt.Errorf("%s in %s: %w", name, from, err)
			}
		}
	}
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
// nothing, which is the cluster's own default.
func count(written any) int {
	if n, ok := written.(int); ok && n >= 0 {
		return n
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
