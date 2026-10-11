package capacity

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Object is one thing a cluster holds, as Kubernetes writes it.
type Object = map[string]any

// Snapshot is a site as its scheduler sees it: the machines, what runs on
// each, the volumes that hold a pod to a machine, and the classes that rank
// work. It is what a change is tried against.
//
// It is published where anybody can read it, so it is made by keeping, not
// by removing: only the fields a scheduler reads are copied out of the
// cluster's own listings, and every name among them is replaced. A machine
// becomes its role and number, and any other name or label value becomes a
// counter, the same one wherever it appears, so that whatever selects a
// thing by name still selects it. Nothing has to be found and scrubbed,
// because nothing that was not asked for is here.
//
// The exceptions are names the repository itself spells: the classes, and
// the namespaces a site's applications run in. An application's pods are
// drawn again from its manifests when a change is tried, and for that the
// snapshot has to say which workload each of its pods belongs to.
type Snapshot struct {
	Taken   string   `json:"read_at"`
	Objects []Object `json:"objects"`
}

// Listings are a cluster's own lists, as `kubectl get -o json` gives them.
type Listings struct {
	Nodes, Pods, Namespaces, Volumes, Claims, PriorityClasses, StorageClasses []byte
}

// What a snapshot writes on a pod about where it came from.
const (
	// OwnerNote is the kind of thing that made the pod.
	OwnerNote = "capacity/owner"
	// WorkloadNote is the workload the pod belongs to, as Kind/name, said
	// only in a namespace whose name the repository spells.
	WorkloadNote = "capacity/workload"
	// EveryMachine is the kind of owner whose pods run one to a machine,
	// and are not made again for a machine that is gone.
	EveryMachine = "DaemonSet"
	// BoundNote marks a claim as bound to its volume, the way the cluster's
	// own volume controller marks it.
	BoundNote = "pv.kubernetes.io/bind-completed"
)

// names replaces names, the same way every time one is met.
type names struct {
	to   map[string]string
	open map[string]bool
}

func (n *names) of(v string) string {
	if v == "" {
		return ""
	}
	if to, known := n.to[v]; known {
		return to
	}
	to := fmt.Sprintf("x%d", len(n.to)+1)
	n.to[v] = to
	return to
}

// namespace is a namespace's name: kept where the repository spells it.
func (n *names) namespace(v string) string {
	if n.open[v] {
		return v
	}
	return n.of(v)
}

// within is a name inside a namespace: kept where the namespace's is.
func (n *names) within(namespace, v string) string {
	if n.open[namespace] {
		return v
	}
	return n.of(v)
}

// Take makes the snapshot of a cluster from its listings. open is the
// namespaces whose names the repository spells.
func Take(l Listings, open []string, taken string) (Snapshot, error) {
	n := &names{to: map[string]string{}, open: map[string]bool{}}
	for _, o := range open {
		n.open[o] = true
	}
	lists := map[string][]Object{}
	for kind, raw := range map[string][]byte{
		"nodes": l.Nodes, "pods": l.Pods, "namespaces": l.Namespaces, "volumes": l.Volumes,
		"claims": l.Claims, "priority classes": l.PriorityClasses, "storage classes": l.StorageClasses,
	} {
		var list struct{ Items []Object }
		if err := json.Unmarshal(raw, &list); err != nil {
			return Snapshot{}, fmt.Errorf("the list of %s is not what kubectl gives: %w", kind, err)
		}
		lists[kind] = list.Items
	}

	// Machines first, so that a machine's name is its role and number
	// wherever it is met afterwards: on a pod, in a volume, in a label.
	workers := 0
	for _, node := range lists["nodes"] {
		name := str(at(node, "metadata", "name"))
		role := "wk"
		if _, is := dict(at(node, "metadata", "labels"))[ControlPlaneLabel]; is {
			role = "cp"
		} else {
			workers++
		}
		parts := strings.Split(name, "-")
		short := role + "-" + parts[len(parts)-1]
		for taken := map[string]bool{}; ; short += "x" {
			for _, to := range n.to {
				taken[to] = true
			}
			if !taken[short] {
				break
			}
		}
		n.to[name] = short
	}
	if workers == 0 {
		return Snapshot{}, fmt.Errorf("the cluster lists %d machine(s) and none is a worker, so there is nothing to hold a change against", len(lists["nodes"]))
	}

	s := Snapshot{Taken: taken}
	for _, c := range lists["priority classes"] {
		s.Objects = append(s.Objects, kept("scheduling.k8s.io/v1", KindPriorityClass, Object{"name": at(c, "metadata", "name")}, nil,
			"value", c["value"], "globalDefault", c["globalDefault"], "preemptionPolicy", c["preemptionPolicy"]))
	}
	for _, c := range lists["storage classes"] {
		s.Objects = append(s.Objects, kept("storage.k8s.io/v1", "StorageClass", Object{"name": at(c, "metadata", "name")}, nil,
			"provisioner", "x", "volumeBindingMode", c["volumeBindingMode"], "allowedTopologies", n.selectors(c["allowedTopologies"])))
	}
	for _, ns := range lists["namespaces"] {
		name := str(at(ns, "metadata", "name"))
		s.Objects = append(s.Objects, kept("v1", "Namespace", Object{"name": n.namespace(name), "labels": n.values(at(ns, "metadata", "labels"))}, nil))
	}
	for _, node := range lists["nodes"] {
		s.Objects = append(s.Objects, kept("v1", "Node",
			Object{"name": n.of(str(at(node, "metadata", "name"))), "labels": n.values(at(node, "metadata", "labels"))},
			pruned(Object{"taints": n.taints(at(node, "spec", "taints")), "unschedulable": at(node, "spec", "unschedulable")}),
			"status", pruned(Object{"allocatable": at(node, "status", "allocatable"), "capacity": at(node, "status", "capacity")})))
	}
	for _, v := range lists["volumes"] {
		ref := dict(at(v, "spec", "claimRef"))
		var claim any
		if len(ref) > 0 {
			ns := str(ref["namespace"])
			claim = Object{"namespace": n.namespace(ns), "name": n.within(ns, str(ref["name"]))}
		}
		s.Objects = append(s.Objects, kept("v1", KindVolume, Object{"name": n.of(str(at(v, "metadata", "name")))}, pruned(Object{
			"capacity": at(v, "spec", "capacity"), "accessModes": at(v, "spec", "accessModes"),
			StorageClassKey: at(v, "spec", StorageClassKey), "volumeMode": at(v, "spec", "volumeMode"),
			"claimRef": claim, NodeAffinityKey: n.selectors(at(v, "spec", NodeAffinityKey)),
			// Every volume has to be of some kind, and which is nothing a
			// scheduler reads.
			"hostPath": Object{"path": "/x"},
		})))
	}
	for _, c := range lists["claims"] {
		ns := str(at(c, "metadata", "namespace"))
		meta := Object{"namespace": n.namespace(ns), "name": n.within(ns, str(at(c, "metadata", "name")))}
		volume := str(at(c, "spec", "volumeName"))
		if volume != "" {
			meta["annotations"] = Object{BoundNote: "yes"}
		}
		s.Objects = append(s.Objects, kept("v1", KindClaim, meta, pruned(Object{
			"accessModes": at(c, "spec", "accessModes"), StorageClassKey: at(c, "spec", StorageClassKey),
			"volumeName": n.of(volume), "volumeMode": at(c, "spec", "volumeMode"),
			"resources": pruned(Object{"requests": at(c, "spec", "resources", "requests")}),
		})))
	}
	for _, p := range lists["pods"] {
		if phase := str(at(p, "status", "phase")); phase != "Running" && phase != "Pending" {
			continue
		}
		ns := str(at(p, "metadata", "namespace"))
		notes := Object{}
		owners, _ := at(p, "metadata", "ownerReferences").([]any)
		if len(owners) > 0 {
			owner := dict(owners[0])
			kind, name := str(owner["kind"]), str(owner["name"])
			notes[OwnerNote] = kind
			if n.open[ns] {
				// A Deployment's pods are made by a ReplicaSet it names
				// after itself, with one more part on the end.
				if kind == "ReplicaSet" {
					if cut := strings.LastIndex(name, "-"); cut > 0 {
						kind, name = "Deployment", name[:cut]
					}
				}
				notes[WorkloadNote] = kind + "/" + name
			}
		}
		spec := Scheduling(dict(p["spec"]), n.of, n.namespace, func(claim string) string { return n.within(ns, claim) })
		if machine := str(at(p, "spec", "nodeName")); machine != "" {
			spec["nodeName"] = n.of(machine)
		}
		s.Objects = append(s.Objects, kept("v1", KindPod, pruned(Object{
			"namespace": n.namespace(ns), "name": n.of(ns + "/" + str(at(p, "metadata", "name"))),
			"labels": n.values(at(p, "metadata", "labels")), "annotations": pruned(notes),
		}), spec))
	}
	return s, nil
}

// Scheduling is the part of a pod a scheduler reads, and nothing else of
// it: what it reserves, its class, where it may and may not go, and the
// claims that hold it to a volume. value replaces a label's value or a
// machine's name, namespace a namespace's, and claim a claim's.
func Scheduling(spec Object, value, namespace func(string) string, claim func(string) string) Object {
	renamed := func(v any) any { return walk(v, value, namespace) }
	out := Object{}
	for _, key := range []string{PriorityClassKey, "overhead", "hostNetwork"} {
		out[key] = spec[key]
	}
	if selector := dict(spec["nodeSelector"]); len(selector) > 0 {
		to := Object{}
		for k, v := range selector {
			to[k] = value(str(v))
		}
		out["nodeSelector"] = to
	}
	out["affinity"] = renamed(spec["affinity"])
	out["topologySpreadConstraints"] = renamed(spec["topologySpreadConstraints"])
	if tolerations, _ := spec["tolerations"].([]any); len(tolerations) > 0 {
		var to []any
		for _, t := range tolerations {
			tol := dict(t)
			to = append(to, pruned(Object{"key": tol["key"], "operator": tol["operator"], "effect": tol["effect"],
				"value": value(str(tol["value"])), "tolerationSeconds": tol["tolerationSeconds"]}))
		}
		out["tolerations"] = to
	}
	for _, list := range []string{InitContainersKey, "containers"} {
		containers, _ := spec[list].([]any)
		var to []any
		for i, c := range containers {
			container := dict(c)
			one := Object{"name": fmt.Sprintf("c%d", i), "image": "x",
				"resources":     pruned(Object{"requests": at(container, "resources", "requests")}),
				"restartPolicy": container["restartPolicy"]}
			// A port taken on the machine itself is a thing two pods
			// cannot both have there.
			var ports []any
			declared, _ := container["ports"].([]any)
			for _, p := range declared {
				if port := dict(p); port["hostPort"] != nil {
					ports = append(ports, pruned(Object{"containerPort": port["containerPort"], "hostPort": port["hostPort"], "protocol": port["protocol"]}))
				}
			}
			if len(ports) > 0 {
				one["ports"] = ports
			}
			to = append(to, pruned(one))
		}
		if len(to) > 0 {
			out[list] = to
		}
	}
	var volumes []any
	declared, _ := spec["volumes"].([]any)
	for i, v := range declared {
		if name := str(at(dict(v), ClaimKey, "claimName")); name != "" {
			volumes = append(volumes, Object{"name": fmt.Sprintf("v%d", i), ClaimKey: Object{"claimName": claim(name)}})
		}
	}
	if len(volumes) > 0 {
		out["volumes"] = volumes
	}
	return pruned(out)
}

// walk is a selector with every value in it replaced: what `values` and
// `matchLabels` hold, and the namespaces a rule about other pods names.
// Keys, operators and everything else are as they were. A value compared
// as a number is left alone, since a counter would compare as nothing.
func walk(v any, value, namespace func(string) string) any {
	switch t := v.(type) {
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = walk(t[i], value, namespace)
		}
		return out
	case map[string]any:
		out := Object{}
		numeric := t["operator"] == "Gt" || t["operator"] == "Lt"
		for k, inner := range t {
			switch {
			case k == "values" && !numeric:
				out[k] = each(inner, value)
			case k == "namespaces":
				out[k] = each(inner, namespace)
			case k == "matchLabels":
				to := Object{}
				for label, val := range dict(inner) {
					to[label] = value(str(val))
				}
				out[k] = to
			default:
				out[k] = walk(inner, value, namespace)
			}
		}
		return out
	}
	return v
}

// selectors is a selector over machines or namespaces, renamed.
func (n *names) selectors(v any) any {
	if v == nil {
		return nil
	}
	return walk(v, n.of, n.namespace)
}

// values is a set of labels with each value renamed and each key kept.
func (n *names) values(labels any) any {
	in := dict(labels)
	if len(in) == 0 {
		return nil
	}
	out := Object{}
	for k, v := range in {
		out[k] = n.of(str(v))
	}
	return out
}

func (n *names) taints(v any) any {
	list, _ := v.([]any)
	var out []any
	for _, t := range list {
		taint := dict(t)
		out = append(out, pruned(Object{"key": taint["key"], "effect": taint["effect"], "value": n.of(str(taint["value"]))}))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func each(list any, rename func(string) string) any {
	in, _ := list.([]any)
	out := make([]any, len(in))
	for i := range in {
		out[i] = rename(str(in[i]))
	}
	return out
}

// kept is an object of a kind, holding what it was given and nothing empty.
func kept(version, kind string, meta, spec Object, more ...any) Object {
	out := Object{"apiVersion": version, "kind": kind, "metadata": pruned(meta)}
	if len(spec) > 0 {
		out["spec"] = spec
	}
	for i := 0; i+1 < len(more); i += 2 {
		if !empty(more[i+1]) {
			out[str(more[i])] = more[i+1]
		}
	}
	return out
}

// pruned is a mapping without what it holds nothing for.
func pruned(o Object) Object {
	for k, v := range o {
		if empty(v) {
			delete(o, k)
		}
	}
	return o
}

func empty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case map[string]any:
		return len(t) == 0
	case []any:
		return len(t) == 0
	}
	return false
}

func at(o Object, path ...string) any {
	var v any = o
	for _, key := range path {
		v = dict(v)[key]
	}
	return v
}

func dict(v any) Object {
	o, _ := v.(map[string]any)
	return o
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// Of is the snapshot's objects of one kind.
func (s Snapshot) Of(kind string) []Object {
	var out []Object
	for _, o := range s.Objects {
		if o["kind"] == kind {
			out = append(out, o)
		}
	}
	return out
}

// Workers is the machines work is placed on, by name, in order.
func (s Snapshot) Workers() []string {
	var out []string
	for _, node := range s.Of("Node") {
		if _, is := dict(at(node, "metadata", "labels"))[ControlPlaneLabel]; !is {
			out = append(out, str(at(node, "metadata", "name")))
		}
	}
	sort.Strings(out)
	return out
}

// String is the site as it stands, for whoever took the snapshot: what each
// worker holds for pods and what is reserved on it.
func (s Snapshot) String() string {
	reserved := map[string]int64{}
	for _, p := range s.Of(KindPod) {
		containers, _ := at(p, "spec", "containers").([]any)
		for _, c := range containers {
			if n, err := Bytes(str(at(dict(c), "resources", "requests", "memory"))); err == nil {
				reserved[str(at(p, "spec", "nodeName"))] += n
			}
		}
	}
	holds := map[string]string{}
	for _, node := range s.Of("Node") {
		holds[str(at(node, "metadata", "name"))] = str(at(node, "status", "allocatable", "memory"))
	}
	var lines []string
	for _, w := range s.Workers() {
		held, _ := Bytes(holds[w])
		lines = append(lines, fmt.Sprintf("%s holds %d MiB for pods and %d is reserved on it", w, held>>20, reserved[w]>>20))
	}
	return strings.Join(lines, "; ")
}

// Marshal is the snapshot as the file holds it.
func Marshal(s Snapshot) ([]byte, error) { return json.MarshalIndent(s, "", " ") }

// Unmarshal reads the file back.
func Unmarshal(raw []byte) (Snapshot, error) {
	var s Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return Snapshot{}, fmt.Errorf("the site's capacity file is not what a record holds: %w", err)
	}
	if len(s.Of("Node")) == 0 {
		return Snapshot{}, fmt.Errorf("the site's capacity file holds no machine. It was written before a change was tried against the site's own scheduler; the next converge writes one that can be")
	}
	return s, nil
}
