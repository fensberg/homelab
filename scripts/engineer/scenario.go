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

// offControlPlanes keeps a pod standing in for a raised setting on the
// workers. Which pod a setting belongs to is not known, so where it may go
// is not either, and what is known is the estate's own rule: work goes on
// the workers. Left free, a stand-in was placed on a control plane that the
// workload it stands for would never be given.
var offControlPlanes = capacity.Object{capacity.NodeAffinityKey: capacity.Object{
	capacity.RequiredKey: capacity.Object{capacity.SelectorTermsKey: []any{
		capacity.Object{capacity.MatchExpressionsKey: []any{capacity.Object{"key": capacity.ControlPlaneLabel, "operator": capacity.HasNoLabel}}},
	}},
}}

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
// stopped to give room back, and is never a reason there is none. So is a
// runner made for one job, which the record caught mid-run.
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
			// A record outlives the program that took it, and the first one
			// taken of a real site kept labels a namespace is refused with.
			o = withLabels(o, capacity.Selectable(meta["labels"]))
		case "Node":
			if name == gone {
				continue
			}
		case capacity.KindClaim:
			claims[namespace+"/"+name] = true
		case capacity.KindPod:
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
		add(capacity.Object{"apiVersion": "v1", "kind": capacity.KindVolume, "metadata": capacity.Object{"name": volume},
			"spec": capacity.Object{"capacity": capacity.Object{"storage": size}, "accessModes": c.Spec["accessModes"],
				capacity.StorageClassKey: c.Spec[capacity.StorageClassKey], "hostPath": capacity.Object{"path": "/x"},
				"claimRef": capacity.Object{"namespace": c.Namespace, "name": c.Name}}})
		add(capacity.Object{"apiVersion": "v1", "kind": capacity.KindClaim,
			"metadata": capacity.Object{"namespace": c.Namespace, "name": c.Name, "annotations": capacity.Object{capacity.BoundNote: "yes"}},
			"spec": capacity.Object{"accessModes": c.Spec["accessModes"], capacity.StorageClassKey: c.Spec[capacity.StorageClassKey], "volumeName": volume,
				"resources": capacity.Object{"requests": capacity.Object{"storage": size}}}})
	}

	// Where each pod of an application is today, in the order the snapshot
	// holds them.
	today := map[string][]string{}
	for _, o := range s.Of(capacity.KindPod) {
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
		// A runner made for one job is how busy the site was when it was
		// recorded, not something it runs: one queued behind another is a
		// queue, not a pod with nowhere to go. What is tried instead is a
		// runner for each place runners are sent, drawn from the tree.
		if owner == capacity.OneJob {
			continue
		}
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
		add(distinctlyNamed(o))
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
		add(capacity.Object{"apiVersion": "v1", "kind": capacity.KindPod,
			"metadata": capacity.Object{"namespace": p.Namespace, "name": name, "labels": p.Labels}, "spec": spec})
	}

	for _, o := range onEachMachine(t.Objects, before, now, false) {
		meta := dict(o["metadata"])
		t.Called[fmt.Sprint(meta["namespace"], "/", meta["name"])] = fmt.Sprint(dict(meta["annotations"])[calledNote])
		add(o)
	}

	// What a setting was raised by, as pods of that much. Where a setting
	// asks for more pods than it did, the new ones are whole.
	for _, name := range sortedNames(now.Core) {
		is, had := now.Core[name], before.Core[name]
		if is.CanWait {
			continue
		}
		// One that runs on every machine was tried on every machine above.
		if _, everywhere := now.Each[name]; everywhere {
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
			add(capacity.Object{"apiVersion": "v1", "kind": capacity.KindPod, "metadata": capacity.Object{"namespace": more, "name": pod},
				"spec": capacity.Object{capacity.PriorityClassKey: mustRun, "affinity": offControlPlanes,
					"containers": []any{capacity.Object{"name": "c0", "image": "x",
						"resources": capacity.Object{"requests": capacity.Object{"memory": fmt.Sprint(extra)}}}}}})
		}
	}
	return t
}

// withLabels is an object with these labels and no others.
func withLabels(o capacity.Object, labels capacity.Object) capacity.Object {
	out := capacity.Object{}
	for k, v := range o {
		out[k] = v
	}
	meta := capacity.Object{}
	for k, v := range dict(o["metadata"]) {
		if k != "labels" {
			meta[k] = v
		}
	}
	if len(labels) > 0 {
		meta["labels"] = labels
	}
	out["metadata"] = meta
	return out
}

// distinctlyNamed is a pod whose containers each have a name of their own,
// by the list each is in and its place there. A snapshot names them so now.
// The first taken of a real site gave a pod's first container and its first
// init container the same name, which a pod is refused for, and a record
// outlives the program that took it.
func distinctlyNamed(o capacity.Object) capacity.Object {
	out := capacity.Object{}
	for k, v := range o {
		out[k] = v
	}
	spec := capacity.Object{}
	for k, v := range dict(o["spec"]) {
		spec[k] = v
	}
	for _, list := range []string{capacity.InitContainersKey, "containers"} {
		containers, _ := spec[list].([]any)
		if len(containers) == 0 {
			continue
		}
		named := make([]any, len(containers))
		for i, c := range containers {
			one := capacity.Object{}
			for k, v := range dict(c) {
				one[k] = v
			}
			one["name"] = fmt.Sprintf("%c%d", list[0], i)
			named[i] = one
		}
		spec[list] = named
	}
	out["spec"] = spec
	return out
}

// calledNote is where a pod standing in for something carries what to call
// it when it is said to wait.
const calledNote = "engineer/called"

// onEachMachine is what a workload that runs on every machine was raised by,
// as one pod of that much held to each machine in a trial.
//
// A setting raised on such a workload is not that much more for the
// scheduler to place where there is room. It is that much taken from every
// machine, and the question is whether each machine still holds what it
// must: the network agent was given a reservation on every machine, and on
// one worker that put an application off the only machine its volume lets
// it run on (#694, #695). So each pod is held to its machine by the
// machine's own name, keeps the workload's class - which is what lets it
// put other work off - and its rules for where it may go.
//
// Which machines the workload runs on is its rules' to say and is not
// worked out here. A machine it does not run on leaves its pod waiting
// whatever is asked, so the trial of the site before the change is given
// the same pods asking for nothing (probe), and a pod that waits in both is
// the change's doing in neither.
func onEachMachine(objects []capacity.Object, before, now asks, probe bool) []capacity.Object {
	var machines []string
	for _, o := range objects {
		if o["kind"] == "Node" {
			machines = append(machines, fmt.Sprint(dig(o, "metadata", "name")))
		}
	}
	sort.Strings(machines)
	var out []capacity.Object
	for n, name := range sortedNames(now.Core) {
		scheduled, everywhere := now.Each[name]
		is, had := now.Core[name], before.Core[name]
		if !everywhere || is.CanWait {
			continue
		}
		extra := is.PodBytes
		if !had.CanWait {
			extra -= had.PodBytes
		}
		if extra <= 0 {
			continue
		}
		asked := fmt.Sprint(extra)
		if probe {
			asked = "0"
		}
		for _, machine := range machines {
			// Its class and its rules about machines, and nothing of its
			// rules about other pods. The pod it stands for is on the
			// machine already, beside whatever it has to be beside; a rule
			// that it be near one pod or apart from another is answered by
			// that pod and not by this one, and names pods by labels a
			// snapshot does not keep.
			spec := capacity.Object{}
			for k, v := range scheduled {
				switch k {
				case "containers", capacity.InitContainersKey, "volumes", capacity.SpreadKey:
				case "affinity":
					if machines := dict(v)[capacity.NodeAffinityKey]; machines != nil {
						spec[k] = capacity.Object{capacity.NodeAffinityKey: machines}
					}
				default:
					spec[k] = v
				}
			}
			// Held to its machine, and to whatever else the workload asks
			// of a machine - but not to what kind of machine it is. Every
			// machine of a site is the kind its workloads run on, and a
			// record taken before a snapshot kept those words holds them
			// replaced, where no rule would find them.
			held := capacity.Object{capacity.HostnameLabel: machine}
			for k, v := range dict(scheduled[capacity.NodeSelectorKey]) {
				if !capacity.AsWritten[k] {
					held[k] = v
				}
			}
			spec[capacity.NodeSelectorKey] = held
			if spec[capacity.PriorityClassKey] == nil {
				spec[capacity.PriorityClassKey] = mustRun
			}
			spec["containers"] = []any{capacity.Object{"name": "c0", "image": "x",
				"resources": capacity.Object{"requests": capacity.Object{"memory": asked}}}}
			out = append(out, capacity.Object{"apiVersion": "v1", "kind": capacity.KindPod,
				"metadata": capacity.Object{"namespace": more, "name": fmt.Sprintf("each-%d-%s", n, machine),
					"annotations": capacity.Object{calledNote: fmt.Sprintf("what %s asks for beyond what it did on %s, %d MiB", name, machine, extra>>20)}},
				"spec": spec})
		}
	}
	return out
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
