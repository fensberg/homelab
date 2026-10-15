package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"homelab/details/capacity"
)

// The programs a trial is run with. None is the engineer's own: the cluster
// is Kubernetes' store, API server and scheduler, at the version the site
// runs, started and stopped by the tool Kubernetes' own project keeps for
// trying a scheduler against machines that are not there.
const (
	starter   = "kwokctl"
	asker     = "kubectl"
	store     = "etcd"
	apiServer = capacity.APIServer
	scheduler = capacity.Scheduler
)

// pause is how long the scheduler is given between two looks at what it
// has done.
var pause = time.Second

// judge has a scheduler place what a trial holds, and answers with every
// pod it left with no machine and why, in the scheduler's words.
type judge func(t trial) (map[string]string, error)

// evicted is said of a pod the scheduler removed to make a place for work
// that may not wait. On the site whatever made the pod makes it again, and
// it is then a pod with nowhere to go.
const evicted = "the scheduler put it off its machine to make a place for work that may not wait, and it has not been placed again"

// realScheduler starts a cluster that holds nothing and runs nothing, loads
// a trial into it and reads what the scheduler did.
//
// Three programs and no more. There is no kubelet, so nothing is run; no
// controllers, so nothing is made or mended behind the trial's back; and the
// scheduler is the real one, so its answer is the one the site would give.
func realScheduler(t trial) (map[string]string, error) {
	dir, env, stop, err := trialCluster()
	if err != nil {
		return nil, err
	}
	defer stop()

	// Everything but the pods first, so a pod finds its machine, its class
	// and its claim already there. Every namespace is given the account a
	// pod is run as when it names none, which a controller would have made.
	var standing, pods []capacity.Object
	for _, o := range t.Objects {
		switch o["kind"] {
		case capacity.KindPod:
			pods = append(pods, o)
		case "Namespace":
			standing = append(standing, o, capacity.Object{"apiVersion": "v1", "kind": "ServiceAccount",
				"metadata": capacity.Object{"name": "default", "namespace": dig(o, "metadata", "name")}})
		default:
			standing = append(standing, o)
		}
	}
	load := func(file string, objects []capacity.Object) error {
		raw, err := json.Marshal(capacity.Object{"apiVersion": "v1", "kind": "List", "items": objects})
		if err != nil {
			return err
		}
		path := filepath.Join(dir, file)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			return err
		}
		_, err = asking(asker, env, "apply", "--server-side", "--force-conflicts", "-f", path)
		return err
	}
	if err := load("standing.json", standing); err != nil {
		return nil, fmt.Errorf("the site could not be loaded into the cluster a change is tried in: %w", err)
	}
	// A machine is marked not ready as it is made, for a controller to
	// unmark once its kubelet reports. There is neither here, so each is
	// given the taints the site's own machine has, and no others.
	for _, o := range standing {
		if o["kind"] != "Node" {
			continue
		}
		patch, err := json.Marshal(capacity.Object{"spec": capacity.Object{"taints": dig(o, "spec", "taints")}})
		if err != nil {
			return nil, err
		}
		if _, err := asking(asker, env, "patch", "node", fmt.Sprint(dig(o, "metadata", "name")), "--type", "merge", "-p", string(patch)); err != nil {
			return nil, err
		}
	}
	if err := load("pods.json", pods); err != nil {
		return nil, fmt.Errorf("the pods could not be loaded into the cluster a change is tried in: %w", err)
	}
	return verdicts(func() ([]byte, error) {
		return asking(asker, env, "get", "pods", capacity.EveryNamespace, "-o", "json")
	}, pods, pause, 90)
}

// trialCluster starts a cluster that holds nothing and runs nothing, and
// gives where it works, what each program needs to find it, and how it is
// stopped and removed, which is to be done whatever happens.
func trialCluster() (dir string, env []string, stop func(), err error) {
	dir, err = os.MkdirTemp("", "engineer-trial-")
	if err != nil {
		return "", nil, nil, err
	}
	name := filepath.Base(dir)
	kubeconfig := filepath.Join(dir, "kubeconfig")
	// What each program needs to find the cluster.
	env = []string{"KWOK_WORKDIR=" + filepath.Join(dir, "work"), "KUBECONFIG=" + kubeconfig}

	start := []string{"create", "cluster", "--name", name, "--runtime", "binary",
		"--components", strings.Join([]string{store, apiServer, scheduler}, ","), "--kubeconfig", kubeconfig}
	// Each program is the one already delivered, where there is one: the
	// starter would otherwise fetch its own, unpinned, from the network.
	for _, program := range []string{store, apiServer, scheduler} {
		if path, err := exec.LookPath(program); err == nil {
			start = append(start, "--"+program+"-binary", path)
		}
	}
	if _, err := asking(starter, env, start...); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, nil, fmt.Errorf("the cluster a change is tried in could not be started: %w", err)
	}
	// If it will not stop there is nothing to do about it here, and the
	// directory it lives in is removed in any case.
	stop = func() {
		_, _ = asking(starter, env, "delete", "cluster", "--name", name)
		_ = os.RemoveAll(dir)
	}
	return dir, env, stop, nil
}

// asking runs one of the trial's programs with what it needs to find the
// cluster, and gives what it printed. What it complained of last is the
// error's, since that is where a program says why.
func asking(program string, env []string, args ...string) ([]byte, error) {
	// proved by TestEveryProgramRunIsNamedByAConstantOrIsTheOperators
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	cmd := exec.Command(program, args...)
	cmd.Env = append(os.Environ(), env...)
	var complaints bytes.Buffer
	cmd.Stderr = &complaints
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", program, strings.Join(args, " "), err, lastLine(complaints.String()))
	}
	return out, nil
}

// verdicts waits until the scheduler has said something of every pod with no
// machine, and gives what it said. A pod that was loaded and is gone, or is
// on its way out, was evicted.
func verdicts(list func() ([]byte, error), loaded []capacity.Object, pause time.Duration, tries int) (map[string]string, error) {
	type listed struct {
		Items []struct {
			Metadata struct {
				Namespace, Name   string
				DeletionTimestamp *string
			}
			Spec   struct{ NodeName string }
			Status struct {
				NominatedNodeName string
				Conditions        []struct{ Type, Message string }
			}
		}
	}
	read := func() (waiting map[string]string, unjudged int, err error) {
		raw, err := list()
		if err != nil {
			return nil, 0, err
		}
		var now listed
		if err := json.Unmarshal(raw, &now); err != nil {
			return nil, 0, fmt.Errorf("the list of pods in the cluster a change is tried in is not what kubectl gives: %w", err)
		}
		waiting = map[string]string{}
		seen := map[string]bool{}
		for _, p := range now.Items {
			id := p.Metadata.Namespace + "/" + p.Metadata.Name
			seen[id] = true
			switch {
			case p.Metadata.DeletionTimestamp != nil:
				waiting[id] = evicted
			// A machine has been chosen and is being cleared for it.
			case p.Spec.NodeName != "" || p.Status.NominatedNodeName != "":
			default:
				said := ""
				for _, c := range p.Status.Conditions {
					if c.Type == "PodScheduled" {
						said = c.Message
					}
				}
				if said == "" {
					unjudged++
				}
				waiting[id] = said
			}
		}
		for _, o := range loaded {
			if id := fmt.Sprint(dig(o, "metadata", "namespace"), "/", dig(o, "metadata", "name")); !seen[id] {
				waiting[id] = evicted
			}
		}
		return waiting, unjudged, nil
	}
	for i := 0; i < tries; i++ {
		_, unjudged, err := read()
		if err != nil {
			return nil, err
		}
		if unjudged == 0 {
			// Once more after a pause: making a place for one pod puts
			// another off its machine, a moment later.
			time.Sleep(3 * pause)
			waiting, again, err := read()
			if err != nil {
				return nil, err
			}
			if again == 0 {
				return waiting, nil
			}
		}
		time.Sleep(pause)
	}
	return nil, fmt.Errorf("the scheduler had not said where every pod goes after %s, so there is no verdict to give", time.Duration(tries)*pause)
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// placedSomewhere refuses a trial in which a workload that runs on every
// machine could be placed on none of them while asking for nothing.
//
// A machine the workload is not on leaves its pod waiting, and that is how
// such a machine is left out. But a pod that waits on every machine is not a
// workload that runs nowhere: it is a rule the trial could not follow, and
// leaving every machine out would pass whatever the change asked for. It was
// found that way, on a real site, the day this was written: an impossible
// raise went through in silence.
func placedSomewhere(standing trial, waiting map[string]string) error {
	asked, waits := map[string]int{}, map[string]int{}
	why := map[string]string{}
	for _, o := range standing.Objects {
		called, _ := dig(o, "metadata", "annotations", calledNote).(string)
		if called == "" {
			continue
		}
		// What it is called says the workload first, and then the machine.
		workload, _, _ := strings.Cut(called, " asks for beyond what it did on ")
		asked[workload]++
		id := fmt.Sprint(dig(o, "metadata", "namespace"), "/", dig(o, "metadata", "name"))
		if said, waited := waiting[id]; waited {
			waits[workload]++
			why[workload] = said
		}
	}
	for workload, n := range asked {
		if waits[workload] == n {
			return fmt.Errorf("%s could be placed on no machine at all, even asking for nothing, so what it was raised by cannot be tried: %s", strings.TrimPrefix(workload, "what "), why[workload])
		}
	}
	return nil
}

// held is every worker a change left work waiting on the loss of, with what
// waits and why. A pod that waited before the change is not the change's
// doing and is left out: a site already short is not made safer by stopping
// a change that asks for no more.
func held(s capacity.Snapshot, before, now asks, place judge) ([]string, error) {
	var out []string
	for _, gone := range append([]string{""}, s.Workers()...) {
		// The site before the change, with a pod asking for nothing wherever
		// the change raises something that runs on every machine: a machine
		// that workload does not run on leaves its pod waiting in both.
		standing := scenario(s, gone, before, before)
		standing.Objects = append(standing.Objects, onEachMachine(standing.Objects, before, now, true)...)
		was, err := place(standing)
		if err != nil {
			return nil, err
		}
		if err := placedSomewhere(standing, was); err != nil {
			return nil, err
		}
		trial := scenario(s, gone, before, now)
		is, err := place(trial)
		if err != nil {
			return nil, err
		}
		var lines []string
		for id, why := range is {
			if _, waited := was[id]; waited {
				continue
			}
			called := trial.Called[id]
			if called == "" {
				called = "a pod"
			}
			lines = append(lines, fmt.Sprintf("  %s: %s", called, why))
		}
		if len(lines) == 0 {
			continue
		}
		sort.Strings(lines)
		when := "With every machine running"
		if gone != "" {
			when = "With " + gone + " gone"
		}
		out = append(out, when+", this change leaves waiting:\n"+strings.Join(lines, "\n"))
	}
	return out, nil
}
