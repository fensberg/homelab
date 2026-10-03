package phases

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Which machines leave, in what order, and which one this job may not touch.
//
// A machine is leaving when the cluster has it and the config no longer asks
// for it. The cluster is asked, not the state: a machine in the state that
// never joined holds no work and no vote, and destroying it is all there is to
// do. docs/epochs/05-node-lifecycle.md has the design.

// machine is one node of the cluster, as Kubernetes lists it.
type machine struct {
	Name         string
	IP           string
	ControlPlane bool
}

// controlPlaneLabel is on every control-plane node, with no value.
const controlPlaneLabel = "node-role.kubernetes.io/control-plane"

// parseMachines reads `kubectl get nodes -o json`.
//
// A node with no internal address is an error. It cannot be compared with the
// config, and reading it as a machine that stays would let the machine under
// it be destroyed with its work still on it.
func parseMachines(out []byte) ([]machine, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				Addresses []struct {
					Type    string `json:"type"`
					Address string `json:"address"`
				} `json:"addresses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("parsing kubectl output: %w", err)
	}
	if len(list.Items) == 0 {
		return nil, fmt.Errorf("the cluster listed no nodes, so which machines are leaving is not known")
	}
	machines := make([]machine, 0, len(list.Items))
	for _, item := range list.Items {
		m := machine{Name: item.Metadata.Name}
		_, m.ControlPlane = item.Metadata.Labels[controlPlaneLabel]
		for _, a := range item.Status.Addresses {
			if a.Type == "InternalIP" {
				m.IP = a.Address
				break
			}
		}
		if m.IP == "" {
			return nil, fmt.Errorf("the node %s reports no internal address, so whether the config still asks for it is not known", m.Name)
		}
		machines = append(machines, m)
	}
	return machines, nil
}

// leaving is the machines the cluster has and the config does not ask for, in
// the order they are retired: workers, then control planes, each by name.
//
// Workers first because retiring one cannot move quorum, so the control plane
// is whole while work is being moved off machines.
func leaving(have []machine, wanted []string) []machine {
	asked := make(map[string]bool, len(wanted))
	for _, ip := range wanted {
		asked[ip] = true
	}
	var out []machine
	for _, m := range have {
		if !asked[m.IP] {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ControlPlane != out[j].ControlPlane {
			return !out[i].ControlPlane
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// exceptOwn splits what is leaving into what this job retires and the one
// machine it hands to the next job: its own.
//
// A job cannot move itself. Draining the machine under it ends the job
// partway, holding whatever it held.
func exceptOwn(out []machine, own string) (now []machine, handed *machine) {
	for i, m := range out {
		if own != "" && m.Name == own {
			handed = &out[i]
			continue
		}
		now = append(now, m)
	}
	return now, handed
}

// ownMachineVar is the variable a runner's pod carries its node's name in.
const ownMachineVar = "CONTRACTOR_NODE_NAME"

// ownMachine is the node this process runs on, or "" outside a cluster.
//
// Inside one and not told is refused. The machine could be one that is
// leaving, and nothing here can find out.
func ownMachine(getenv func(string) string) (string, error) {
	if getenv("KUBERNETES_SERVICE_HOST") == "" {
		return "", nil
	}
	own := getenv(ownMachineVar)
	if own == "" {
		return "", fmt.Errorf(`this converge runs in a pod and was not told which node the pod is on.

A machine that is leaving is drained, and a converge on that machine would end
partway. %s is how a runner's pod says where it is; it is unset`, ownMachineVar)
	}
	return own, nil
}
