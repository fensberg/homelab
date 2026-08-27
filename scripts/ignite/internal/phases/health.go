package phases

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"homelab/ignite/internal/config"
	"homelab/ignite/internal/run"
)

// Health waits for the cluster to actually converge, and fails the run if it
// does not.
//
// The first full ignition of this project reported success over a state
// database running two of its three instances. Every phase had done its job:
// the VMs came up, Talos bootstrapped, Flux reconciled, Postgres answered on
// its port and the state migrated into it. Nobody asked whether the cluster
// was healthy, so nobody found out for days.
//
// A port that answers is the weakest evidence available - it is true from the
// moment one instance is up. This phase asks the three questions that would
// have caught it: are all the nodes Ready, did everything Flux manages
// reconcile, and does the database have the instances it was asked for.
//
// It sits before Migrate deliberately. Moving state into a database is the
// point of no return for this run; doing it into a degraded one is how the
// state describing the cluster ends up on a replica that is not there.
func Health(ctx *run.Context) error {
	run.WritePhase("Health", "Wait for the cluster to converge, and refuse to continue if it does not.")

	kubeconfig, cleanup, err := writeKubeconfig(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := waitFor(ctx, kubeconfig, "nodes", 5*time.Minute, checkNodes); err != nil {
		return err
	}
	if err := waitFor(ctx, kubeconfig, "Flux reconciliation", 15*time.Minute, checkFlux); err != nil {
		return err
	}
	if err := waitFor(ctx, kubeconfig, "the state database", 15*time.Minute, checkDatabase); err != nil {
		return err
	}

	run.Ok("cluster is healthy: nodes ready, Flux reconciled, database at full instance count")
	return nil
}

// A check returns nil when healthy, or an error describing what is not.
type healthCheck func(ctx *run.Context, kubeconfig string) error

// waitFor polls until the check passes or the deadline expires, reporting what
// is still outstanding as it goes. Flux takes minutes on a cold cluster -
// pulling images, establishing CRDs, waiting on its own dependency ordering -
// so the timeouts are generous and the failure, when it comes, names what
// never arrived rather than saying "timed out".
func waitFor(ctx *run.Context, kubeconfig, what string, timeout time.Duration, check healthCheck) error {
	run.Info("waiting for " + what + " ...")
	deadline := time.Now().Add(timeout)
	var last error
	for {
		last = check(ctx, kubeconfig)
		if last == nil {
			run.Ok(what + ": ready")
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf(`%s did not become healthy within %s.

%v

The cluster is still running and its state is still local, so nothing has been
lost - this phase refuses to continue rather than migrating state into a
degraded cluster. Look at what is listed above, then re-run from here:

    ./scripts/ignite/ignite -site %s -from health`, what, timeout, last, ctx.Site)
		}
		run.Info("  still waiting: " + firstLine(last.Error()))
		time.Sleep(15 * time.Second)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// --- the checks -------------------------------------------------------------

func checkNodes(ctx *run.Context, kubeconfig string) error {
	out, err := kubectl(ctx, kubeconfig, "get", "nodes", "-o", "json")
	if err != nil {
		return err
	}
	unready, err := notReady(out)
	if err != nil {
		return err
	}
	if len(unready) > 0 {
		return fmt.Errorf("%d node(s) not Ready:\n  %s", len(unready), strings.Join(unready, "\n  "))
	}

	// Counting matters as much as the readiness verdict: three Ready nodes out
	// of a config asking for five is every node healthy and two missing, and
	// "no node is unready" is true in both cases.
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return err
	}
	want, err := expectedNodeCount(ctx)
	if err != nil {
		return err
	}
	if len(list.Items) != want {
		return fmt.Errorf("%d node(s) joined, expected %d", len(list.Items), want)
	}
	return nil
}

func expectedNodeCount(ctx *run.Context) (int, error) {
	cfg, err := config.LoadRendered(ctx.ConfigRendered)
	if err != nil {
		return 0, err
	}
	site, ok := cfg.Sites[ctx.Site]
	if !ok {
		return 0, fmt.Errorf("unknown site '%s'", ctx.Site)
	}
	return site.ControlPlaneCount, nil
}

func checkFlux(ctx *run.Context, kubeconfig string) error {
	out, err := kubectl(ctx, kubeconfig, "get", "kustomizations,helmreleases", "-A", "-o", "json")
	if err != nil {
		return err
	}
	unready, err := notReady(out)
	if err != nil {
		return err
	}
	if len(unready) > 0 {
		return fmt.Errorf("%d Flux resource(s) not reconciled:\n  %s", len(unready), strings.Join(unready, "\n  "))
	}

	// An empty list is not health. It means the CRDs are installed and Flux
	// has not created anything yet, which reads identically to "everything is
	// fine" if you only count failures.
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return err
	}
	if len(list.Items) == 0 {
		return fmt.Errorf("no Kustomizations or HelmReleases exist yet")
	}
	return nil
}

func checkDatabase(ctx *run.Context, kubeconfig string) error {
	out, err := kubectl(ctx, kubeconfig, "get", "clusters.postgresql.cnpg.io", "-A", "-o", "json")
	if err != nil {
		return err
	}
	ready, want, err := databaseInstances(out)
	if err != nil {
		return err
	}
	if ready != want {
		return fmt.Errorf(`the state database has %d of %d instances ready.

This is the exact failure the first ignition of this project shipped over. A
degraded CloudNativePG cluster still answers on its port, so every other signal
in the run stays green. If an instance is stuck Pending, storage is the usual
reason - check that the OpenEBS hostpath volume actually mounted on every
node`, ready, want)
	}
	return nil
}

// --- parsing ----------------------------------------------------------------

// notReady returns a human-readable line for every item in a Kubernetes list
// whose Ready condition is not True.
//
// An item with no Ready condition counts as not ready. Reading "no verdict" as
// "fine" is how a gate passes before the thing it gates on has begun.
func notReady(body []byte) ([]string, error) {
	var list struct {
		Items []struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Status struct {
				Conditions []struct {
					Type    string `json:"type"`
					Status  string `json:"status"`
					Message string `json:"message"`
					Reason  string `json:"reason"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("parsing kubectl output: %w", err)
	}

	var out []string
	for _, item := range list.Items {
		name := item.Metadata.Name
		if item.Metadata.Namespace != "" {
			name = item.Metadata.Namespace + "/" + name
		}
		if item.Kind != "" {
			name = item.Kind + " " + name
		}

		found := false
		for _, c := range item.Status.Conditions {
			if c.Type != "Ready" {
				continue
			}
			found = true
			if c.Status != "True" {
				detail := c.Message
				if detail == "" {
					detail = c.Reason
				}
				if detail == "" {
					detail = "not ready"
				}
				out = append(out, name+": "+detail)
			}
		}
		if !found {
			out = append(out, name+": no Ready condition yet")
		}
	}
	return out, nil
}

// databaseInstances reports how many CloudNativePG instances are ready against
// how many were asked for. Absent status fields are zero, never "as many as we
// wanted", and no Cluster at all is an error rather than a vacuous pass.
func databaseInstances(body []byte) (ready, want int, err error) {
	var list struct {
		Items []struct {
			Spec struct {
				Instances int `json:"instances"`
			} `json:"spec"`
			Status struct {
				ReadyInstances int `json:"readyInstances"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return 0, 0, fmt.Errorf("parsing kubectl output: %w", err)
	}
	if len(list.Items) == 0 {
		return 0, 0, fmt.Errorf("no CloudNativePG Cluster exists yet - Flux has not created the state database")
	}
	for _, c := range list.Items {
		ready += c.Status.ReadyInstances
		want += c.Spec.Instances
	}
	return ready, want, nil
}

// --- talking to the cluster -------------------------------------------------

// writeKubeconfig materialises the kubeconfig for the lifetime of one phase.
//
// kubectl, unlike the kubernetes and talos providers used everywhere else
// here, cannot authenticate from in-memory values - it needs a file. So this
// is the same shape gitops.tf's local-exec already uses: a 0600 file that is
// removed when the caller is done, never a kubeconfig left in the home
// directory. Sterilize would not catch that one, because it is not in the
// workspace.
func writeKubeconfig(ctx *run.Context) (path string, cleanup func(), err error) {
	raw, err := run.TofuOutputRaw(ctx, "kubeconfig")
	if err != nil {
		return "", func() {}, fmt.Errorf("could not read the kubeconfig output. Has the Cluster phase run? (%w)", err)
	}
	f, err := os.CreateTemp("", "ignite-kubeconfig-*")
	if err != nil {
		return "", func() {}, err
	}
	path = f.Name()
	cleanup = func() { _ = os.Remove(path) }

	if err := f.Chmod(0o600); err != nil {
		f.Close()
		cleanup()
		return "", func() {}, err
	}
	if _, err := f.WriteString(raw); err != nil {
		f.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
}

func kubectl(ctx *run.Context, kubeconfig string, args ...string) ([]byte, error) {
	out, err := run.CmdOutputEnv(ctx.ClusterDir, []string{"KUBECONFIG=" + kubeconfig}, "kubectl", args...)
	if err != nil {
		return nil, fmt.Errorf("kubectl %s: %w", strings.Join(args, " "), err)
	}
	return []byte(out), nil
}

// WriteKubeconfigTo materialises the kubeconfig at a chosen path, for a human
// who wants to look at the cluster.
//
// Deliberately separate from writeKubeconfig above: that one exists for the
// lifetime of a single check and removes itself. This one persists, which is
// the whole point, and is therefore a workspace file that Sterilize owns.
func WriteKubeconfigTo(ctx *run.Context, dest string) error {
	raw, err := run.TofuOutputRaw(ctx, "kubeconfig")
	if err != nil {
		return fmt.Errorf("could not read the kubeconfig output. Has the Cluster phase run? (%w)", err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(dest, []byte(raw), 0o600); err != nil {
		return err
	}
	run.Ok("kubeconfig written to " + dest)
	run.Warn("It is a credential and it is gitignored, but it is still on this disk. 'task clean-secrets' removes it.")
	return nil
}
