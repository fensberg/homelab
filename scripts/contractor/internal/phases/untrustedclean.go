package phases

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"homelab/contractor/config"
	"homelab/contractor/internal/run"
	"homelab/contractor/steps"
	"homelab/details/capacity"
	"homelab/details/hypervisorapi"
	"homelab/details/onepassword"
)

// A machine in an untrusted zone is put back, after it is used, to a
// snapshot of its disk (docs/epochs/08-agent-roles.md). This is where that
// snapshot comes from, and the rule is one line: a zone's machine that has
// no clean snapshot is given one, before it takes any work, and a machine
// that has one is never given another.
//
// It can be that simple because a zone's machine is never changed where it
// stands. Any change to it builds another (the cluster module), and so does
// the sweeper deleting one it could not put back; either way what arrives
// is a new machine with no snapshot, which is the only kind there is to
// find. And the snapshot is of a machine that has run nothing: it joins
// with a taint nothing tolerates, and that taint is lifted here, after the
// snapshot and by nothing else.
//
// NOT IN THE SNAPSHOT YET: the image the pull-request runners start from.
// A machine that was put back fetches it again before its first runner
// starts. Putting it on the machine before the snapshot is to do.

// untrustedGuests is what this asks of the hypervisor.
type untrustedGuests interface {
	List() ([]hypervisorapi.Guest, error)
	HasClean(hypervisorapi.Guest) (bool, error)
	Shutdown(hypervisorapi.Guest) error
	TakeClean(hypervisorapi.Guest) error
	Start(hypervisorapi.Guest) error
}

// untrustedHypervisor is the site's hypervisor as the converge's own token
// reaches it, and the names of the site's untrusted workers. A variable so
// that a test stands something in for the hypervisor.
var untrustedHypervisor = func(ctx *run.Context) (untrustedGuests, []string, error) {
	cfg, err := config.LoadRendered(ctx.ConfigRendered)
	if err != nil {
		return nil, nil, err
	}
	site, ok := cfg.Sites[ctx.Site]
	if !ok {
		return nil, nil, fmt.Errorf("the config has no site %q", ctx.Site)
	}
	net, err := config.ResolveSiteNetwork(cfg, ctx.Site)
	if err != nil {
		return nil, nil, err
	}
	authority, err := onepassword.Read(hypervisorapi.AuthorityRef(ctx.Site))
	if err != nil {
		return nil, nil, err
	}
	keys := make([]string, 0, len(site.Hypervisor.Nodes))
	for key := range site.Hypervisor.Nodes {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	if len(keys) == 0 {
		return nil, nil, fmt.Errorf("site %q has no hypervisor to ask", ctx.Site)
	}
	node := site.Hypervisor.Nodes[keys[0]]
	client, err := hypervisorapi.Client(authority, node.Hostname, 30*time.Second)
	if err != nil {
		return nil, nil, err
	}
	return hypervisorapi.Guests{
		Client: client, Base: fmt.Sprintf("https://%s:8006/api2/json", node.IP),
		TokenID: site.Hypervisor.TokenID, TokenSecret: site.Hypervisor.TokenSecret,
	}, net.DMZNames, nil
}

// How long a machine that was started is given to be ready, and how often
// it is looked at.
var (
	backPause = 5 * time.Second
	backLooks = 60
)

// withoutASnapshot is the site's untrusted workers that have no clean
// snapshot: the new ones. A machine the config names and the hypervisor
// does not have is an error here, because both callers come after the step
// that builds it.
func withoutASnapshot(hv untrustedGuests, names []string) ([]hypervisorapi.Guest, error) {
	guests, err := hv.List()
	if err != nil {
		return nil, err
	}
	var fresh []hypervisorapi.Guest
	for _, name := range names {
		at := slices.IndexFunc(guests, func(g hypervisorapi.Guest) bool { return g.Name == name })
		if at < 0 {
			return nil, fmt.Errorf("the hypervisor has no machine for one of the site's untrusted workers, after the step that builds them")
		}
		has, err := hv.HasClean(guests[at])
		if err != nil {
			return nil, err
		}
		if !has {
			fresh = append(fresh, guests[at])
		}
	}
	return fresh, nil
}

// untrustedNodes is what this asks of the cluster about one machine's node.
type untrustedNodes interface {
	Node(name string) (untrustedNode, error)
	Forget(name string) error
	Open(name string) error
}

// forgetReplacedUntrustedWorkers removes the node a new machine's predecessor
// left, before the new machine is given its configuration and can join, and
// says which configurations are to be sent again: each new machine's.
//
// The taint that keeps work off a machine with no snapshot is put on when
// its node is made, and a machine that joins as a node which is already
// there is given no taint at all. A replacement has its predecessor's name,
// so the predecessor's node is removed first, and the machine has not
// joined yet because it has not been configured yet.
func forgetReplacedUntrustedWorkers(ctx *run.Context) ([]string, error) {
	cfg, err := config.LoadRendered(ctx.ConfigRendered)
	if err != nil {
		return nil, err
	}
	net, err := config.ResolveSiteNetwork(cfg, ctx.Site)
	if err != nil {
		return nil, err
	}
	var replace []string
	err = withUntrusted(ctx, func(hv untrustedGuests, fresh []hypervisorapi.Guest, nodes untrustedNodes) error {
		if err := forgetReplaced(hv, fresh, nodes); err != nil {
			return err
		}
		replace, err = configurationsOf(fresh, net.DMZKeys)
		return err
	})
	return replace, err
}

// configurationsOf is the address of each new machine's configuration, by
// the key the address plan gives it.
func configurationsOf(fresh []hypervisorapi.Guest, keys map[string]string) ([]string, error) {
	var out []string
	for _, m := range fresh {
		key, ok := keys[m.Name]
		if !ok {
			return nil, fmt.Errorf("an untrusted worker on the hypervisor is not one the address plan names, so which configuration is its cannot be said")
		}
		out = append(out, fmt.Sprintf("%s[%q]", steps.ZoneConfig, key))
	}
	return out, nil
}

// cleanNewUntrustedWorkers gives every untrusted worker that has no clean
// snapshot its one, and then opens it to work.
func cleanNewUntrustedWorkers(ctx *run.Context) error {
	return withUntrusted(ctx, cleanNew)
}

// withUntrusted hands one of the two acts the hypervisor, the site's untrusted
// machines that have no clean snapshot, and the cluster - and reaches for
// the cluster only when there is such a machine.
func withUntrusted(ctx *run.Context, act func(untrustedGuests, []hypervisorapi.Guest, untrustedNodes) error) error {
	hv, names, err := untrustedHypervisor(ctx)
	if err != nil {
		return err
	}
	fresh, err := withoutASnapshot(hv, names)
	if err != nil || len(fresh) == 0 {
		return err
	}
	kubeconfig, cleanup, err := writeKubeconfig(ctx.In(ctx.Cluster))
	if err != nil {
		return err
	}
	defer cleanup()
	return act(hv, fresh, kubectlNodes{ctx, kubeconfig})
}

func forgetReplaced(_ untrustedGuests, fresh []hypervisorapi.Guest, nodes untrustedNodes) error {
	for _, m := range fresh {
		if err := nodes.Forget(m.Name); err != nil {
			return err
		}
	}
	return nil
}

func cleanNew(hv untrustedGuests, fresh []hypervisorapi.Guest, nodes untrustedNodes) error {
	run.Info(fmt.Sprintf("taking the clean snapshot of %d new untrusted worker(s)", len(fresh)))
	for _, m := range fresh {
		before, err := nodes.Node(m.Name)
		if err != nil {
			return err
		}
		// Refused, not repaired: a machine open to work with no snapshot
		// may have run something, and a snapshot of it would make whatever
		// that left the state every later job is promised is clean.
		if !before.closed {
			return fmt.Errorf("an untrusted worker has no clean snapshot and is open to work, so nothing says it has run nothing. Delete it on the hypervisor and converge again; the converge builds another")
		}
		if err := hv.Shutdown(m); err != nil {
			return err
		}
		if err := hv.TakeClean(m); err != nil {
			return err
		}
		if err := hv.Start(m); err != nil {
			return err
		}
		back := false
		for look := 0; look < backLooks && !back; look++ {
			if look > 0 {
				time.Sleep(backPause)
			}
			now, err := nodes.Node(m.Name)
			if err != nil {
				return err
			}
			back = now.ready && now.boot != "" && now.boot != before.boot
		}
		if !back {
			return fmt.Errorf("an untrusted worker did not come back ready after its clean snapshot was taken; it stays closed to work")
		}
		if err := nodes.Open(m.Name); err != nil {
			return err
		}
	}
	run.Ok("every untrusted worker has a clean snapshot to be put back to")
	return nil
}

// kubectlNodes is the cluster as the converge reaches it.
type kubectlNodes struct {
	ctx        *run.Context
	kubeconfig string
}

// Forget removes a node, when there is one: a worker built for the first
// time has no predecessor and so no node to remove.
func (k kubectlNodes) Forget(name string) error {
	out, err := kubectl(k.ctx, k.kubeconfig, "get", "nodes", "-o", "name")
	if err != nil {
		return err
	}
	if !slices.Contains(strings.Fields(string(out)), "node/"+name) {
		return nil
	}
	_, err = kubectl(k.ctx, k.kubeconfig, "delete", "node", name)
	return err
}

func (k kubectlNodes) Open(name string) error {
	_, err := kubectl(k.ctx, k.kubeconfig, "taint", "node", name, capacity.NotYetClean+"-")
	return err
}

func (k kubectlNodes) Node(name string) (untrustedNode, error) {
	out, err := kubectl(k.ctx, k.kubeconfig, "get", "node", name, "-o", "json")
	if err != nil {
		return untrustedNode{}, err
	}
	return untrustedNodeFrom(out)
}

type untrustedNode struct {
	ready, closed bool
	boot          string
}

// untrustedNodeFrom reads one node's description: whether it is ready, which
// start of its machine this is, and whether it still carries the taint it
// joined with.
func untrustedNodeFrom(out []byte) (untrustedNode, error) {
	var node struct {
		Spec struct {
			Taints []struct {
				Key string `json:"key"`
			} `json:"taints"`
		} `json:"spec"`
		Status struct {
			NodeInfo struct {
				BootID string `json:"bootID"`
			} `json:"nodeInfo"`
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	}
	if err := json.Unmarshal(out, &node); err != nil {
		return untrustedNode{}, fmt.Errorf("a node's description is not the shape this reads: %w", err)
	}
	state := untrustedNode{boot: node.Status.NodeInfo.BootID}
	for _, t := range node.Spec.Taints {
		state.closed = state.closed || t.Key == capacity.NotYetClean
	}
	for _, c := range node.Status.Conditions {
		state.ready = state.ready || (c.Type == "Ready" && c.Status == "True")
	}
	return state, nil
}

// configuresZones reports whether a step hands the zones' machines their
// configuration, which is the moment a new one can first join.
func configuresZones(st steps.Step) bool {
	return slices.Contains(st.Targets, steps.ZoneConfig)
}
