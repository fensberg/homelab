package phases

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"homelab/contractor/config"
	"homelab/contractor/internal/run"
	"homelab/details/kube"
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

// What kubectl is asked to print, so that nothing here decodes a node: a line
// per machine holding its name and its internal address, and the names of the
// control planes.
const (
	machineLines = `jsonpath={range .items[*]}{.metadata.name}{" "}{.status.addresses[?(@.type=="InternalIP")].address}{"\n"}{end}`
	machineNames = `jsonpath={.items[*].metadata.name}`
	claimLines   = `jsonpath={range .items[*]}{.metadata.namespace}/{.metadata.name}{"\n"}{end}`
)

// parseMachines reads the cluster's machines from machineLines, and which of
// them are control planes from machineNames asked of those alone.
//
// A machine with no internal address is an error. It cannot be compared with
// the config, and reading it as a machine that stays would let the machine
// under it be destroyed with its work still on it.
func parseMachines(lines, controlPlanes string) ([]machine, error) {
	isControlPlane := map[string]bool{}
	for _, name := range strings.Fields(controlPlanes) {
		isControlPlane[name] = true
	}
	var machines []machine
	for _, line := range strings.Split(lines, "\n") {
		fields := strings.Fields(line)
		switch len(fields) {
		case 0:
			continue
		case 2:
			machines = append(machines, machine{Name: fields[0], IP: fields[1], ControlPlane: isControlPlane[fields[0]]})
		default:
			return nil, fmt.Errorf("the node %s does not report exactly one internal address, so whether the config still asks for it is not known", fields[0])
		}
	}
	if len(machines) == 0 {
		return nil, fmt.Errorf("the cluster listed no nodes, so which machines are leaving is not known")
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

// HandedOver is how the retire phase ends when the one machine left to retire
// is the one this job runs on. It is not a failure: everything else is done,
// that machine is cordoned, and the next job - on a pod Kubernetes cannot put
// there - runs the same converge and finishes.
type HandedOver struct {
	Machine string
}

func (h *HandedOver) Error() string {
	return fmt.Sprintf("%s is the machine this job runs on, so it is cordoned and left for the next job", h.Machine)
}

// How long one machine's work may take to leave it, and how long what was on
// it may take to be whole again somewhere else.
const (
	drainTimeout   = 15 * time.Minute
	rebuildTimeout = 15 * time.Minute
	etcdTimeout    = 5 * time.Minute
)

// Retire takes out of the cluster every machine the config no longer asks
// for, before the compute phase lets OpenTofu destroy it.
//
// A machine OpenTofu destroys with work on it loses the work, and a control
// plane it destroys stays a member of etcd that never answers again. So each
// machine is emptied and removed first, one at a time, and the cluster is
// asked whether it is whole before the next.
func Retire(ctx *run.Context) error {
	run.WritePhase("Retire", "Empty and remove every machine the config no longer asks for.")

	cfg, err := config.LoadRendered(ctx.ConfigRendered)
	if err != nil {
		return err
	}
	net, err := config.ResolveSiteNetwork(cfg, ctx.Site)
	if err != nil {
		return err
	}
	kubeconfig, cleanup, err := writeKubeconfig(ctx.In(ctx.Cluster))
	if err != nil {
		return err
	}
	defer cleanup()

	r := &retirement{ctx: ctx, kubeconfig: kubeconfig, net: net}
	out, err := r.leaving()
	if err != nil {
		return err
	}
	if len(out) == 0 {
		run.Ok("no machine is leaving")
		return nil
	}

	if _, err := exec.LookPath("talosctl"); err != nil {
		return fmt.Errorf("%d machine(s) are leaving and talosctl is not on PATH, so none can be taken out of the cluster. Nothing has changed", len(out))
	}
	talosconfig, cleanupTalos, err := writeTalosconfig(ctx.In(ctx.Cluster))
	if err != nil {
		return err
	}
	defer cleanupTalos()
	r.talosconfig = talosconfig

	own, err := ownMachine(os.Getenv)
	if err != nil {
		return err
	}
	return r.retire(out, own)
}

// retirement is one run of the phase: the cluster it asks, and the site the
// config describes.
type retirement struct {
	ctx         *run.Context
	kubeconfig  string
	talosconfig string
	net         *config.SiteNetwork
}

// machines asks the cluster which machines it has.
func (r *retirement) machines() ([]machine, error) {
	lines, err := kubectl(r.ctx, r.kubeconfig, "get", "nodes", "-o", machineLines)
	if err != nil {
		return nil, fmt.Errorf("could not ask the cluster which machines it has, so which are leaving is not known: %w", err)
	}
	controlPlanes, err := kubectl(r.ctx, r.kubeconfig, "get", "nodes", "-l", kube.ControlPlaneLabel, "-o", machineNames)
	if err != nil {
		return nil, fmt.Errorf("could not ask the cluster which machines are control planes: %w", err)
	}
	return parseMachines(string(lines), string(controlPlanes))
}

func (r *retirement) leaving() ([]machine, error) {
	have, err := r.machines()
	if err != nil {
		return nil, err
	}
	return leaving(have, r.net.AllMachineIPs()), nil
}

// retire refuses before it touches anything, then takes the machines out one
// at a time.
func (r *retirement) retire(out []machine, own string) error {
	run.Info("leaving: " + machineList(out))
	if len(r.net.NodeIPs) == 0 {
		return fmt.Errorf("the config asks for no control plane, so there would be no cluster to take a machine out of")
	}

	dbs, err := r.databases()
	if err != nil {
		return err
	}
	for _, db := range dbs {
		if db.Instances < 2 {
			return fmt.Errorf(`the database %s/%s has one instance.

Its operator moves an instance off a machine by building another from a
second, and refuses to drain a machine holding the only one. Give it two or
more instances before a machine leaves. Nothing has changed`, db.Namespace, db.Name)
		}
	}
	for _, m := range out {
		if err := r.volumesAreSomebodys(m); err != nil {
			return err
		}
	}

	// Every workload that said how takes a backup now: what is on a leaving
	// machine's disk does not outlive it.
	if err := SaveWorkloads(r.ctx); err != nil {
		return fmt.Errorf("a backup could not be taken, so no machine has been retired: %w", err)
	}

	now, handed := exceptOwn(out, own)
	controlPlanes, err := r.controlPlanes()
	if err != nil {
		return err
	}
	for _, m := range now {
		if err := r.retireOne(m, dbs, &controlPlanes); err != nil {
			return err
		}
	}
	if handed == nil {
		run.Ok(fmt.Sprintf("%d machine(s) retired", len(now)))
		return nil
	}
	if _, err := kubectl(r.ctx, r.kubeconfig, "cordon", handed.Name); err != nil {
		return fmt.Errorf("%s is the machine this job runs on, and it could not be cordoned, so the next job could land on it too: %w", handed.Name, err)
	}
	return &HandedOver{Machine: handed.Name}
}

// retireOne empties one machine, takes it out of etcd and out of Kubernetes,
// and waits for the cluster to be whole without it.
func (r *retirement) retireOne(m machine, dbs []database, controlPlanes *int) error {
	run.Info("retiring " + m.Name)
	if m.ControlPlane {
		// Asked before, not only after: taking a member out of an etcd that
		// already has one unwell is how quorum is lost on purpose.
		if err := r.etcdHas(*controlPlanes); err != nil {
			return fmt.Errorf("etcd is not sound, so %s stays: %w", m.Name, err)
		}
	}

	// Tell each database's operator the machine is not coming back, so it
	// builds the instance again elsewhere and does not wait for the old disk.
	if err := r.maintenance(dbs, true); err != nil {
		return err
	}
	defer func() {
		if err := r.maintenance(dbs, false); err != nil {
			run.Warn(err.Error())
		}
	}()

	if _, err := kubectl(r.ctx, r.kubeconfig, "drain", m.Name,
		"--ignore-daemonsets", "--delete-emptydir-data", "--timeout="+drainTimeout.String()); err != nil {
		return fmt.Errorf(`%s could not be emptied (%w).

It is cordoned and still running, with whatever would not move still on it. A
disruption budget that cannot be met is the usual reason. Nothing has been
destroyed`, m.Name, err)
	}
	if err := waitFor(r.ctx, r.kubeconfig, "every database whole again without "+m.Name, rebuildTimeout, checkDatabase); err != nil {
		return err
	}

	// Graceful: the machine leaves etcd itself, then wipes its disks and
	// powers off. It was drained above, with the drain that is known to
	// honour a disruption budget.
	if _, err := run.CmdOutputEnv(r.ctx.Dir, []string{"TALOSCONFIG=" + r.talosconfig},
		"talosctl", "--nodes", m.IP, "reset", "--graceful=true", "--wait=true"); err != nil {
		return fmt.Errorf("%s is empty and could not be reset, so it is still a member of the cluster: %w", m.Name, err)
	}
	if _, err := kubectl(r.ctx, r.kubeconfig, "delete", "node", m.Name); err != nil {
		return fmt.Errorf("%s was reset and its node could not be deleted: %w", m.Name, err)
	}
	if m.ControlPlane {
		*controlPlanes--
		want := *controlPlanes
		if err := waitFor(r.ctx, r.kubeconfig, fmt.Sprintf("etcd at %d voting member(s)", want), etcdTimeout,
			func(*run.Context, string) error { return r.etcdHas(want) }); err != nil {
			return err
		}
	}
	run.Ok(m.Name + " is out of the cluster")
	return nil
}

// controlPlanes is how many control planes the cluster has now.
func (r *retirement) controlPlanes() (int, error) {
	have, err := r.machines()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range have {
		if m.ControlPlane {
			n++
		}
	}
	return n, nil
}

// etcdHas asks a control plane that is staying whether etcd has exactly this
// many members and every one votes.
func (r *retirement) etcdHas(want int) error {
	out, err := run.CmdOutputEnv(r.ctx.Dir, []string{"TALOSCONFIG=" + r.talosconfig},
		"talosctl", "--nodes", r.net.NodeIPs[0], "etcd", "members")
	if err != nil {
		return fmt.Errorf("could not read etcd membership from the cluster: %w", err)
	}
	members, err := parseEtcdMembers(out)
	if err != nil {
		return err
	}
	return judgeEtcd(members, want)
}

// database is one CloudNativePG cluster.
type database struct {
	Namespace string
	Name      string
	Instances int
}

func parseDatabases(body []byte) ([]database, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Namespace string `json:"namespace"`
				Name      string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Instances int `json:"instances"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("parsing kubectl output: %w", err)
	}
	dbs := make([]database, 0, len(list.Items))
	for _, c := range list.Items {
		dbs = append(dbs, database{Namespace: c.Metadata.Namespace, Name: c.Metadata.Name, Instances: c.Spec.Instances})
	}
	return dbs, nil
}

func (r *retirement) databases() ([]database, error) {
	body, err := kubectl(r.ctx, r.kubeconfig, "get", "clusters.postgresql.cnpg.io", "-A", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("could not list the databases, so whether one would lose its only instance is not known: %w", err)
	}
	return parseDatabases(body)
}

// maintenance opens or closes every database's maintenance window. Open, with
// the volume not to be reused, is its operator's way of being told a machine
// is going for good.
func (r *retirement) maintenance(dbs []database, open bool) error {
	patch := fmt.Sprintf(`{"spec":{"nodeMaintenanceWindow":{"inProgress":%t,"reusePVC":false}}}`, open)
	for _, db := range dbs {
		if _, err := kubectl(r.ctx, r.kubeconfig, "patch", "clusters.postgresql.cnpg.io", db.Name,
			"-n", db.Namespace, "--type", "merge", "-p", patch); err != nil {
			if open {
				return fmt.Errorf("the database %s/%s could not be told a machine is leaving, so no machine has been drained: %w", db.Namespace, db.Name, err)
			}
			return fmt.Errorf("the maintenance window of the database %s/%s is still open, and while it is the database does not heal itself. Close it: %w", db.Namespace, db.Name, err)
		}
	}
	return nil
}

// hostnameKey is how a node-local volume says which machine it is on.
const hostnameKey = "kubernetes.io/hostname"

// volumesAreSomebodys refuses a machine holding a volume nothing will move.
//
// A node-local volume is on one machine's disk. A database's operator builds
// its instance again elsewhere. Anything else on the machine is lost with it,
// and its pod then waits for a disk that no longer exists.
func (r *retirement) volumesAreSomebodys(m machine) error {
	pvs, err := kubectl(r.ctx, r.kubeconfig, "get", "persistentvolumes", "-o", "json")
	if err != nil {
		return fmt.Errorf("could not list the volumes, so what is on %s's disk is not known: %w", m.Name, err)
	}
	owned, err := kubectl(r.ctx, r.kubeconfig, "get", "persistentvolumeclaims", "-A", "-l", kube.DatabaseLabel, "-o", claimLines)
	if err != nil {
		return fmt.Errorf("could not list the databases' volume claims, so what is on %s's disk is not known: %w", m.Name, err)
	}
	stranded, err := strandedClaims(pvs, strings.Fields(string(owned)), m.Name)
	if err != nil {
		return err
	}
	if len(stranded) == 0 {
		return nil
	}
	return fmt.Errorf(`%s holds %d volume(s) nothing here knows how to move:

  %s

Each is on that machine's own disk and would be lost with it. Move or remove
what uses them first. Nothing has changed`, m.Name, len(stranded), strings.Join(stranded, "\n  "))
}

// strandedClaims is the claims bound to a volume on the named machine that no
// database owns, as namespace/name. databases is the claims one does own.
func strandedClaims(pvs []byte, databases []string, node string) ([]string, error) {
	var volumes struct {
		Items []struct {
			Spec struct {
				ClaimRef *struct {
					Namespace string `json:"namespace"`
					Name      string `json:"name"`
				} `json:"claimRef"`
				NodeAffinity struct {
					Required struct {
						NodeSelectorTerms []struct {
							MatchExpressions []struct {
								Key    string   `json:"key"`
								Values []string `json:"values"`
							} `json:"matchExpressions"`
						} `json:"nodeSelectorTerms"`
					} `json:"required"`
				} `json:"nodeAffinity"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(pvs, &volumes); err != nil {
		return nil, fmt.Errorf("parsing kubectl output: %w", err)
	}
	owned := map[string]bool{}
	for _, claim := range databases {
		owned[claim] = true
	}

	var stranded []string
	for _, v := range volumes.Items {
		if v.Spec.ClaimRef == nil {
			continue
		}
		here := false
		for _, term := range v.Spec.NodeAffinity.Required.NodeSelectorTerms {
			for _, e := range term.MatchExpressions {
				if e.Key == hostnameKey && len(e.Values) == 1 && e.Values[0] == node {
					here = true
				}
			}
		}
		claim := v.Spec.ClaimRef.Namespace + "/" + v.Spec.ClaimRef.Name
		if here && !owned[claim] {
			stranded = append(stranded, claim)
		}
	}
	sort.Strings(stranded)
	return stranded, nil
}

func machineList(ms []machine) string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Name
	}
	return strings.Join(out, ", ")
}
