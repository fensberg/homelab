package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"homelab/details/capacity"
)

// What the engineer adds to a site to try a change against it.
const (
	// more is the namespace of the pods that stand in for what a chart's
	// settings were raised by.
	more = "engineer-more"
	// mustRun is the class those stand-ins are given: work that takes the
	// next free place and may not make one.
	mustRun = "interactive"
)

// trial is a site set up for the scheduler to judge: everything to load, in
// the order to load it, and what to call each pod when saying it waits.
type trial struct {
	Objects []capacity.Object
	Called  map[string]string
}

// scenario is the site as a snapshot holds it, with one worker gone and
// with what a tree asks for in place of what the snapshot's applications
// were running.
//
// before is what the tree this is a change from asked for, and the two are
// compared in three places. An application's pods are drawn from its
// manifests in both, so that the two trials differ only by the change. A
// pod the change leaves exactly as it was stays on the machine it is on
// today; one it alters, or adds, is the scheduler's to place. And what a
// chart's settings were raised by is added as pods standing in for it.
//
// A pod on the worker that is gone has to be placed again, unless it is one
// of a set that runs on every machine. Work that can wait is left out: it is
// stopped to give room back, and is never a reason there is none.
func scenario(s capacity.Snapshot, gone string, before, now asks) trial {
	t := trial{Called: map[string]string{}}
	add := func(o capacity.Object) { t.Objects = append(t.Objects, o) }
	drawnAgain := map[string]bool{}
	was := map[string]string{}
	for _, tree := range []asks{before, now} {
		for _, p := range tree.Pods {
			drawnAgain[p.Namespace+"/"+p.Workload] = true
		}
	}
	for _, p := range before.Pods {
		was[podName(p)] = canonical(p)
	}

	namespaces := map[string]bool{}
	claims := map[string]bool{}
	for _, o := range s.Objects {
		meta, _ := o["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		namespace, _ := meta["namespace"].(string)
		switch o["kind"] {
		case "Namespace":
			namespaces[name] = true
		case "Node":
			if name == gone {
				continue
			}
		case "PersistentVolumeClaim":
			claims[namespace+"/"+name] = true
		case "Pod":
			continue
		}
		add(o)
	}
	for _, ns := range append(namespacesOf(now), more) {
		if !namespaces[ns] {
			namespaces[ns] = true
			add(capacity.Object{"apiVersion": "v1", "kind": "Namespace", "metadata": capacity.Object{"name": ns}})
		}
	}
	// A claim the site does not hold yet is taken as one its storage will
	// provide wherever the pod lands, which is what provisioning a new
	// volume does.
	for i, c := range now.Claims {
		if claims[c.Namespace+"/"+c.Name] {
			continue
		}
		claims[c.Namespace+"/"+c.Name] = true
		volume := fmt.Sprintf("engineer-new-%d", i)
		size := dig(c.Spec, "resources", "requests", "storage")
		add(capacity.Object{"apiVersion": "v1", "kind": "PersistentVolume", "metadata": capacity.Object{"name": volume},
			"spec": capacity.Object{"capacity": capacity.Object{"storage": size}, "accessModes": c.Spec["accessModes"],
				"storageClassName": c.Spec["storageClassName"], "hostPath": capacity.Object{"path": "/x"},
				"claimRef": capacity.Object{"namespace": c.Namespace, "name": c.Name}}})
		add(capacity.Object{"apiVersion": "v1", "kind": "PersistentVolumeClaim",
			"metadata": capacity.Object{"namespace": c.Namespace, "name": c.Name, "annotations": capacity.Object{capacity.BoundNote: "yes"}},
			"spec": capacity.Object{"accessModes": c.Spec["accessModes"], "storageClassName": c.Spec["storageClassName"], "volumeName": volume,
				"resources": capacity.Object{"requests": capacity.Object{"storage": size}}}})
	}

	// Where each pod of an application is today, in the order the snapshot
	// holds them.
	today := map[string][]string{}
	for _, o := range s.Of("Pod") {
		meta, _ := o["metadata"].(map[string]any)
		namespace, _ := meta["namespace"].(string)
		notes, _ := meta["annotations"].(map[string]any)
		spec, _ := o["spec"].(map[string]any)
		machine, _ := spec["nodeName"].(string)
		workload, _ := notes[capacity.WorkloadNote].(string)
		if workload != "" && drawnAgain[namespace+"/"+workload] {
			today[namespace+"/"+workload] = append(today[namespace+"/"+workload], machine)
			continue
		}
		if spec[capacity.PriorityClassKey] == capacity.CanWait {
			continue
		}
		owner, _ := notes[capacity.OwnerNote].(string)
		if machine == gone && gone != "" {
			if owner == capacity.EveryMachine {
				continue
			}
			o = unplaced(o)
		}
		if owner == "" {
			owner = "nothing"
		}
		t.Called[namespace+"/"+fmt.Sprint(meta["name"])] = fmt.Sprintf("a pod the site runs today, made by a %s, reserving %s", owner, reserves(spec))
		add(o)
	}
	for _, p := range now.Pods {
		name := podName(p)
		spec := capacity.Object{}
		for k, v := range p.Spec {
			spec[k] = v
		}
		// Left where it is, if the change leaves it as it was.
		if at := today[p.Namespace+"/"+p.Workload]; p.Index < len(at) && at[p.Index] != "" && at[p.Index] != gone && was[name] == canonical(p) {
			spec["nodeName"] = at[p.Index]
		}
		t.Called[p.Namespace+"/"+name] = fmt.Sprintf("%s in %s, reserving %s", p.Workload, p.Namespace, reserves(spec))
		add(capacity.Object{"apiVersion": "v1", "kind": "Pod",
			"metadata": capacity.Object{"namespace": p.Namespace, "name": name, "labels": p.Labels}, "spec": spec})
	}

	// What a setting was raised by, as pods of that much. Where a setting
	// asks for more pods than it did, the new ones are whole.
	for _, name := range sortedNames(now.Core) {
		is, had := now.Core[name], before.Core[name]
		if is.CanWait {
			continue
		}
		for i := 0; i < is.Pods; i++ {
			extra := is.PodBytes
			if i < had.Pods && !had.CanWait {
				extra -= had.PodBytes
			}
			if extra <= 0 {
				continue
			}
			pod := fmt.Sprintf("more-%d-%d", len(t.Called), i)
			t.Called[more+"/"+pod] = fmt.Sprintf("what %s asks for beyond what it did, %d MiB", name, extra>>20)
			add(capacity.Object{"apiVersion": "v1", "kind": "Pod", "metadata": capacity.Object{"namespace": more, "name": pod},
				"spec": capacity.Object{capacity.PriorityClassKey: mustRun, "containers": []any{capacity.Object{"name": "c0", "image": "x",
					"resources": capacity.Object{"requests": capacity.Object{"memory": fmt.Sprint(extra)}}}}}})
		}
	}
	return t
}

// unplaced is a pod with no machine named, for the scheduler to place.
func unplaced(o capacity.Object) capacity.Object {
	out := capacity.Object{}
	for k, v := range o {
		out[k] = v
	}
	spec := capacity.Object{}
	for k, v := range dict(o["spec"]) {
		if k != "nodeName" {
			spec[k] = v
		}
	}
	out["spec"] = spec
	return out
}

// podName is the name a pod of an application goes by in a trial: its
// workload and its number.
func podName(p pod) string {
	return strings.ToLower(strings.ReplaceAll(p.Workload, "/", "-")) + fmt.Sprintf("-%d", p.Index)
}

// canonical is a pod as text, the same for two pods that are the same.
func canonical(p pod) string {
	raw, _ := json.Marshal([]any{p.Labels, p.Spec})
	return string(raw)
}

func namespacesOf(a asks) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range a.Pods {
		if !seen[p.Namespace] {
			seen[p.Namespace] = true
			out = append(out, p.Namespace)
		}
	}
	for _, c := range a.Claims {
		if !seen[c.Namespace] {
			seen[c.Namespace] = true
			out = append(out, c.Namespace)
		}
	}
	sort.Strings(out)
	return out
}

func sortedNames(m map[string]workload) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// reserves is the memory a pod's containers ask for, as each wrote it.
func reserves(spec map[string]any) string {
	var asked []string
	containers, _ := spec["containers"].([]any)
	for _, c := range containers {
		if written := dig(dict(c), "resources", "requests", "memory"); written != nil {
			asked = append(asked, fmt.Sprint(written))
		}
	}
	if len(asked) == 0 {
		return "nothing"
	}
	return strings.Join(asked, " + ")
}

func dict(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func dig(m map[string]any, path ...string) any {
	var v any = m
	for _, key := range path {
		v = dict(v)[key]
	}
	return v
}

// same says two trees ask a site for the same thing, which is the ordinary
// case and needs no trial.
func same(a, b asks) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
