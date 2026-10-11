package capacity

import (
	"encoding/json"
	"strings"
	"testing"
)

// A small site as its cluster lists it: one control plane and two workers,
// named for a place nobody may learn, a namespace the repository spells and
// one it does not, a volume held to one worker, and pods of each kind.
var site = Listings{
	Nodes: []byte(`{"items":[
 {"metadata":{"name":"north-street-office-cp-100","labels":{"node-role.kubernetes.io/control-plane":"","kubernetes.io/hostname":"north-street-office-cp-100"}},
  "spec":{"taints":[{"key":"dedicated","value":"north-street-office","effect":"NoSchedule"}]},
  "status":{"allocatable":{"memory":"3Gi","cpu":"2"},"capacity":{"memory":"4Gi"},"nodeInfo":{"machineID":"abc"},"addresses":[{"address":"10.9.8.7"}]}},
 {"metadata":{"name":"north-street-office-wk-200","labels":{"kubernetes.io/hostname":"north-street-office-wk-200","zone":"quay"}},"status":{"allocatable":{"memory":"8Gi"}}},
 {"metadata":{"name":"north-street-office-wk-201","labels":{"kubernetes.io/hostname":"north-street-office-wk-201","zone":"quay"}},"status":{"allocatable":{"memory":"8Gi"}}}]}`),
	Namespaces: []byte(`{"items":[
 {"metadata":{"name":"ledger","labels":{"kubernetes.io/metadata.name":"ledger","pod-security.kubernetes.io/enforce":"privileged","pod-security.kubernetes.io/warn-version":"latest"}}},
 {"metadata":{"name":"pastime","labels":{"kubernetes.io/metadata.name":"pastime"}}}]}`),
	PriorityClasses: []byte(`{"items":[{"metadata":{"name":"interactive"},"value":10000,"preemptionPolicy":"Never"}]}`),
	StorageClasses:  []byte(`{"items":[{"metadata":{"name":"kept"},"provisioner":"somebody.example/driver","volumeBindingMode":"WaitForFirstConsumer"}]}`),
	Volumes: []byte(`{"items":[{"metadata":{"name":"pvc-1f2e"},"spec":{"capacity":{"storage":"5Gi"},"accessModes":["ReadWriteOnce"],"storageClassName":"kept",
  "claimRef":{"namespace":"pastime","name":"saves","uid":"u"},"csi":{"driver":"somebody.example/driver","volumeHandle":"north-street-office/vm-9"},
  "nodeAffinity":{"required":{"nodeSelectorTerms":[{"matchExpressions":[{"key":"kubernetes.io/hostname","operator":"In","values":["north-street-office-wk-201"]}]}]}}}}]}`),
	Claims: []byte(`{"items":[{"metadata":{"namespace":"pastime","name":"saves"},"spec":{"accessModes":["ReadWriteOnce"],"storageClassName":"kept","volumeName":"pvc-1f2e","resources":{"requests":{"storage":"5Gi"}}}}]}`),
	Pods: []byte(`{"items":[
 {"metadata":{"namespace":"ledger","name":"books-1","labels":{"role":"books"},"annotations":{"password-hint":"north-street-office"},"ownerReferences":[{"kind":"Cluster","name":"books"}]},
  "spec":{"nodeName":"north-street-office-wk-200","priorityClassName":"interactive","serviceAccountName":"books",
   "containers":[{"name":"postgres","image":"registry.invalid/pg:1","env":[{"name":"PGPASSWORD","value":"hunter2"}],"resources":{"requests":{"memory":"256Mi","cpu":"100m"},"limits":{"memory":"512Mi"}}}],
   "affinity":{"podAntiAffinity":{"requiredDuringSchedulingIgnoredDuringExecution":[{"topologyKey":"kubernetes.io/hostname","namespaces":["ledger"],"labelSelector":{"matchLabels":{"role":"books"}}}]}}},
  "status":{"phase":"Running","podIP":"10.1.2.3"}},
 {"metadata":{"namespace":"pastime","name":"world-5d9f7-abcde","ownerReferences":[{"kind":"ReplicaSet","name":"world-5d9f7"}]},
  "spec":{"nodeName":"north-street-office-wk-201","priorityClassName":"interactive","containers":[{"name":"server","image":"i","resources":{"requests":{"memory":"2Gi"}},"ports":[{"containerPort":2456,"hostPort":2456,"protocol":"UDP"},{"containerPort":80}]}],
   "volumes":[{"name":"saves","persistentVolumeClaim":{"claimName":"saves"}},{"name":"token","secret":{"secretName":"world-token"}}]},
  "status":{"phase":"Running"}},
 {"metadata":{"namespace":"ledger","name":"watcher-x","ownerReferences":[{"kind":"DaemonSet","name":"watcher"}]},
  "spec":{"nodeName":"north-street-office-wk-200","containers":[{"name":"w","resources":{}}],"tolerations":[{"key":"dedicated","operator":"Equal","value":"north-street-office","effect":"NoSchedule"}]},"status":{"phase":"Running"}},
 {"metadata":{"namespace":"ledger","name":"finished"},"spec":{"containers":[{"name":"j"}]},"status":{"phase":"Succeeded"}}]}`),
}

func take(t *testing.T) Snapshot {
	t.Helper()
	s, err := Take(site, []string{"pastime"}, "2026-10-11T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The snapshot is published. Nothing of the place, of an address, of a
// secret or of anything a scheduler does not read may be in it.
func TestASnapshotHoldsNothingOfTheSiteButWhatASchedulerReads(t *testing.T) {
	raw, err := Marshal(take(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"north-street-office", "ledger", "books", "hunter2", "PGPASSWORD", "registry.invalid", "10.9.8.7", "10.1.2.3",
		"machineID", "somebody.example", "vm-9", "world-token", "password-hint", "serviceAccountName", "limits", "pvc-1f2e"} {
		if strings.Contains(string(raw), leaked) {
			t.Errorf("the snapshot holds %q, which is not for publishing", leaked)
		}
	}
	back, err := Unmarshal(raw)
	if err != nil || len(back.Objects) != len(take(t).Objects) || back.Taken != "2026-10-11T00:00:00Z" {
		t.Errorf("the snapshot did not come back as it was written: %d objects, %v", len(back.Objects), err)
	}
}

// A name is replaced the same way wherever it is met, or what selects a
// thing by its name stops selecting it.
func TestWhatSelectsAThingByNameStillSelectsItInASnapshot(t *testing.T) {
	s := take(t)
	if got := strings.Join(s.Workers(), " "); got != "wk-200 wk-201" {
		t.Fatalf("the workers are %q, not their roles and numbers", got)
	}
	find := func(kind string, is func(Object) bool) Object {
		t.Helper()
		for _, o := range s.Of(kind) {
			if is(o) {
				return o
			}
		}
		t.Fatalf("no %s of that description is in the snapshot", kind)
		return nil
	}
	text := func(o any) string { raw, _ := json.Marshal(o); return string(raw) }

	// The volume is held to the worker by that worker's own label.
	volume := s.Of(KindVolume)[0]
	if !strings.Contains(text(at(volume, "spec", "nodeAffinity")), `"values":["wk-201"]`) {
		t.Errorf("the volume is not held to wk-201: %s", text(volume))
	}
	worker := find("Node", func(o Object) bool { return at(o, "metadata", "name") == "wk-201" })
	if at(worker, "metadata", "labels", "kubernetes.io/hostname") != "wk-201" {
		t.Errorf("the worker's own label does not say wk-201: %s", text(worker))
	}

	// The claim is bound to that volume, and the pod holds that claim, by
	// the names the repository spells for a namespace it spells.
	claim := s.Of(KindClaim)[0]
	if at(claim, "metadata", "namespace") != "pastime" || at(claim, "metadata", "name") != "saves" ||
		at(claim, "spec", "volumeName") != at(volume, "metadata", "name") || at(claim, "metadata", "annotations", BoundNote) == nil ||
		at(volume, "spec", "claimRef", "name") != "saves" {
		t.Errorf("the claim and its volume do not name each other: %s %s", text(claim), text(volume))
	}
	held := find(KindPod, func(o Object) bool { return at(o, "metadata", "namespace") == "pastime" })
	if !strings.Contains(text(held), `"claimName":"saves"`) || at(held, "metadata", "annotations", WorkloadNote) != "Deployment/world" ||
		at(held, "spec", "nodeName") != "wk-201" || !strings.Contains(text(held), `"hostPort":2456`) || strings.Contains(text(held), `"containerPort":80`) {
		t.Errorf("the application's pod is not the workload, on the machine, holding the claim and the port it was: %s", text(held))
	}

	// A rule about other pods names the label value and the namespace its
	// own pod was given.
	apart := find(KindPod, func(o Object) bool { return at(o, "metadata", "annotations", OwnerNote) == "Cluster" })
	label, namespace := str(at(apart, "metadata", "labels", "role")), str(at(apart, "metadata", "namespace"))
	rule := text(at(apart, "spec", "affinity"))
	if label == "" || label == "books" || !strings.Contains(rule, `"role":"`+label+`"`) || !strings.Contains(rule, `"namespaces":["`+namespace+`"]`) {
		t.Errorf("the rule keeping the pods apart does not name its own pod's label %q and namespace %q: %s", label, namespace, rule)
	}
	if at(apart, "metadata", "annotations", WorkloadNote) != nil {
		t.Errorf("a pod in a namespace the repository does not spell says what workload it is: %s", text(apart))
	}

	// A toleration answers the taint it was written for.
	tainted := find("Node", func(o Object) bool { return at(o, "metadata", "name") == "cp-100" })
	daemon := find(KindPod, func(o Object) bool { return at(o, "metadata", "annotations", OwnerNote) == EveryMachine })
	taint := str(at(dict(at(tainted, "spec", "taints").([]any)[0]), "value"))
	if taint == "" || !strings.Contains(text(at(daemon, "spec", "tolerations")), `"value":"`+taint+`"`) {
		t.Errorf("the toleration does not carry the taint's value %q: %s", taint, text(daemon))
	}

	// A namespace keeps the labels it can be selected by, and none of the
	// ones the API server acts on: those are refused under any other value.
	for _, ns := range s.Of("Namespace") {
		labels := dict(at(ns, "metadata", "labels"))
		if len(labels) != 1 || labels["kubernetes.io/metadata.name"] == nil {
			t.Errorf("a namespace's labels are %v, not the one it is selected by", labels)
		}
	}

	if len(s.Of(KindPod)) != 3 {
		t.Errorf("the snapshot holds %d pods, not the three that are running", len(s.Of(KindPod)))
	}
	if said := s.String(); !strings.Contains(said, "wk-200 holds 8192 MiB for pods and 256 is reserved on it") || !strings.Contains(said, "wk-201 holds 8192 MiB for pods and 2048") {
		t.Errorf("the site says how it stands as %q", said)
	}
}

func TestAClusterThatCannotBeSnapshottedIsAnErrorAndNotAnEmptySite(t *testing.T) {
	onlyControl := site
	onlyControl.Nodes = []byte(`{"items":[{"metadata":{"name":"example-cp-1","labels":{"node-role.kubernetes.io/control-plane":""}}}]}`)
	if _, err := Take(onlyControl, nil, ""); err == nil || !strings.Contains(err.Error(), "nothing to hold a change against") {
		t.Errorf("a cluster with no worker was snapshotted: %v", err)
	}
	broken := site
	broken.Pods = []byte(`not json`)
	if _, err := Take(broken, nil, ""); err == nil || !strings.Contains(err.Error(), "pods") {
		t.Errorf("a list that is not one was read, or not named: %v", err)
	}
	if _, err := Unmarshal([]byte(`{"workers_gib":27}`)); err == nil || !strings.Contains(err.Error(), "written before a change was tried") {
		t.Errorf("a file from before the snapshot was read as one: %v", err)
	}
	if _, err := Unmarshal([]byte(`[`)); err == nil {
		t.Error("a file that is not JSON was read")
	}
}
