package phases

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"homelab/contractor/internal/config"
	"homelab/contractor/internal/run"
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
// moment one instance is up. This phase asks the questions that would have
// caught it: are all the nodes Ready and schedulable, is every per-node
// workload actually running on every node, did everything Flux manages
// reconcile, and does the database have the instances it was asked for.
//
// The per-node question was added after a scale-up. "The machines exist and
// Kubernetes calls them Ready" and "the machines are usable" are different
// claims, and every check here proved only the first: a node can be Ready,
// cordoned, and running nothing at all. DaemonSets are the cheap way to tell
// the difference, because adding a node raises every DaemonSet's desired count
// - so a machine that cannot run the CNI, kube-proxy or the storage
// provisioner shows up here as a shortfall rather than as a mystery weeks
// later. Nothing else in this phase covers them: the Flux check reads
// Kustomizations and HelmReleases, the database check reads CloudNativePG.
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

	for _, c := range healthChecks {
		if err := waitFor(ctx, kubeconfig, c.name, c.timeout, c.check); err != nil {
			return err
		}
	}

	run.Ok("cluster is healthy: nodes ready and schedulable, per-node workloads running on every node, Flux reconciled, database at full instance count")
	return nil
}

// A check returns nil when healthy, or an error describing what is not.
type healthCheck func(ctx *run.Context, kubeconfig string) error

// The checks, as data rather than as a run of if-statements.
//
// A deleted call in a sequence of near-identical blocks is three green lines
// and a smaller file; a deleted entry here is a list that no longer matches
// what health_checks_test.go says this phase asks. That test is the reason for
// the shape - it is not possible to unit-test the phase itself, because every
// check shells out to kubectl.
var healthChecks = []struct {
	name    string
	timeout time.Duration
	check   healthCheck
}{
	{"nodes", 5 * time.Minute, checkNodes},
	{"Flux reconciliation", 15 * time.Minute, checkFlux},
	{"the state database", 15 * time.Minute, checkDatabase},
	{"per-node workloads", 10 * time.Minute, checkDaemonSets},
}

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

    ./scripts/contractor/contractor break-ground -site %s -from health`, what, timeout, last, ctx.Site)
		}
		run.Info("  still waiting: " + summariseWait(last))
		time.Sleep(15 * time.Second)
	}
}

// summariseWait turns a multi-line check failure into one progress line that
// still says which things are outstanding.
//
// It used to print only the first line, which for the Flux check was
// "4 Flux resource(s) not reconciled:" - a colon promising a list that had
// just been cut off. Worse, the count moves in both directions as Flux
// discovers more resources to reconcile, so a run that is progressing normally
// reads as one going backwards. The names are what make that legible.
func summariseWait(err error) string {
	lines := strings.Split(err.Error(), "\n")
	head := strings.TrimSuffix(strings.TrimSpace(lines[0]), ":")

	var items []string
	for _, l := range lines[1:] {
		l = strings.TrimSpace(l)
		// The long-form errors carry an explanation after a blank line; the
		// itemised ones do not. Stop at the first gap either way.
		if l == "" {
			break
		}
		items = append(items, l)
	}
	if len(items) == 0 {
		return head
	}

	joined := strings.Join(items, ", ")
	const cap = 100
	if len(joined) > cap {
		joined = joined[:cap] + "..."
	}
	return head + ": " + joined
}

// --- the checks -------------------------------------------------------------

func checkNodes(ctx *run.Context, kubeconfig string) error {
	out, err := kubectl(ctx, kubeconfig, "get", "nodes", "-o", "json")
	if err != nil {
		return err
	}
	want, err := expectedNodeCount(ctx)
	if err != nil {
		return err
	}
	return nodeVerdict(out, want)
}

// nodeVerdict is every question this phase asks of the node list, in one pure
// function.
//
// Separated from the fetch on purpose. When these lived inline in checkNodes,
// deleting the cordon check broke nothing: the parser it called still had its
// own passing tests, so the assertion could be removed and every test stayed
// green. A pure function that returns the verdict is one a test can hold to
// all three answers at once.
func nodeVerdict(out []byte, want int) error {
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
	if len(list.Items) != want {
		return fmt.Errorf("%d node(s) joined, expected %d", len(list.Items), want)
	}

	// Ready and unschedulable at the same time is an ordinary state - it is
	// what a cordon produces - and it is invisible to the Ready condition,
	// which stays True throughout. A scale-up that lands two cordoned nodes
	// adds two machines the cluster will never place anything on, and reports
	// five healthy nodes while doing it.
	cordoned, err := unschedulable(out)
	if err != nil {
		return err
	}
	if len(cordoned) > 0 {
		return fmt.Errorf("%d node(s) Ready but not schedulable:\n  %s", len(cordoned), strings.Join(cordoned, "\n  "))
	}
	return nil
}

// checkDaemonSets asks whether every per-node workload is running on every
// node it is wanted on.
//
// This is the closest cheap answer to "is that machine usable". A DaemonSet's
// desiredNumberScheduled is what the scheduler wants after taints, tolerations
// and node selectors are taken into account, so comparing it against
// numberReady is meaningful whatever the DaemonSet targets - and it moves the
// moment a node is added.
//
// On this estate that is `kube-system/kube-flannel` and
// `kube-system/kube-proxy`, both of which read 5 of 5 after the scale-up to
// five nodes - measured, not assumed. The storage provisioner is deliberately
// not in that list: OpenEBS Local PV ships its provisioner as a Deployment
// rather than a DaemonSet, so this check says nothing about whether a new node
// can serve a volume. An earlier version of this comment claimed it did.
//
// A new machine that cannot run the CNI or kube-proxy is a new machine nothing
// can use, which is what makes this the cheap answer worth having. It is not
// the complete one.
func checkDaemonSets(ctx *run.Context, kubeconfig string) error {
	out, err := kubectl(ctx, kubeconfig, "get", "daemonsets", "-A", "-o", "json")
	if err != nil {
		return err
	}
	short, err := daemonSetShortfalls(out)
	if err != nil {
		return err
	}
	if len(short) > 0 {
		return fmt.Errorf("%d per-node workload(s) not running everywhere they are wanted:\n  %s",
			len(short), strings.Join(short, "\n  "))
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

// unschedulable names every node carrying spec.unschedulable, which is what a
// cordon sets and what nothing else in this phase reads.
func unschedulable(body []byte) ([]string, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Unschedulable bool `json:"unschedulable"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("parsing kubectl output: %w", err)
	}
	var out []string
	for _, n := range list.Items {
		if n.Spec.Unschedulable {
			out = append(out, n.Metadata.Name+": cordoned, so nothing will be scheduled on it")
		}
	}
	return out, nil
}

// daemonSetShortfalls names every DaemonSet with fewer pods ready than the
// scheduler wants placed.
//
// Absent status fields are zero rather than "as many as we wanted", so a
// DaemonSet whose status has not been written yet reads as a shortfall - which
// is correct, because it has not been shown to be running anywhere. An empty
// list is an error for the same reason the Flux check treats one that way:
// this cluster runs a CNI and a kube-proxy as DaemonSets, so none at all means
// the query was wrong or the cluster is not up, and neither is health.
func daemonSetShortfalls(body []byte) ([]string, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Status struct {
				DesiredNumberScheduled int `json:"desiredNumberScheduled"`
				NumberReady            int `json:"numberReady"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("parsing kubectl output: %w", err)
	}
	if len(list.Items) == 0 {
		return nil, fmt.Errorf("no DaemonSets exist at all - this cluster runs its CNI and kube-proxy that way, so an empty list is a wrong answer rather than a healthy one")
	}
	var out []string
	for _, d := range list.Items {
		if d.Status.NumberReady < d.Status.DesiredNumberScheduled {
			out = append(out, fmt.Sprintf("%s/%s: %d of %d ready",
				d.Metadata.Namespace, d.Metadata.Name,
				d.Status.NumberReady, d.Status.DesiredNumberScheduled))
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
	return writeRenderedCredential(ctx, dest, "kubeconfig", looksLikeKubeconfig)
}

// writeRenderedCredential reads one sensitive tofu output and writes it to
// dest, then removes everything it needed to get there except the file it was
// asked for.
func writeRenderedCredential(
	ctx *run.Context,
	dest string,
	output string,
	looksRight func(string) bool,
) error {
	// Reach the state before reading an output out of it.
	//
	// This used to go straight to `tofu output`, which worked only while a run
	// was in flight. Every other time - which is every time anybody actually
	// wants a credential - Sterilize has already removed backend_pg.tf and
	// tofu's backend record, so there is no state to read.
	//
	// Local state means a run is mid-flight and already holds the
	// authoritative copy, so leave it alone; otherwise the state is where the
	// successful path put it.
	if _, err := os.Stat(ctx.LocalState); err != nil {
		if err := Render(ctx); err != nil {
			return fmt.Errorf("could not render the config, so there is nothing to authenticate with: %w", err)
		}
		if err := Attach(ctx); err != nil {
			return err
		}
	}

	raw, err := run.TofuOutputRaw(ctx, output)
	if err != nil {
		return fmt.Errorf("could not read the %s output. Has the Cluster phase run? (%w)", output, err)
	}

	// Anything of the wrong shape means the output was not what was asked for
	// - a diagnostic, an empty backend, a truncated read - and writing it
	// produces a file that fails much later, inside the tool, with an error
	// naming nothing useful.
	if !looksRight(raw) {
		return fmt.Errorf(`the %s output did not come back as a %s.

What arrived was %d byte(s) that do not parse as one. That usually means the
state could not be reached, so tofu answered with something other than the
value asked for. Check that the cluster is up and that this site's state
database is reachable`, output, output, len(raw))
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(dest, []byte(raw), 0o600); err != nil {
		return err
	}
	// Clean up everything this needed except the thing it was asked for.
	// Attaching writes backend_pg.tf and tofu's backend record, and this verb
	// returns before the sterilize a phase sequence would end with. Left
	// behind, that record makes the next `tofu init -backend=false` fail on
	// encrypted state, which breaks `task validate` and the pre-push hook.
	for _, t := range sterilizeTargets(ctx) {
		if t == dest {
			continue
		}
		if err := run.RemoveIfExists(t); err != nil {
			return err
		}
	}
	return nil
}

// looksLikeKubeconfig is a shape check, not a parse. It exists to fail here,
// naming the output, rather than three commands later inside kubectl.
func looksLikeKubeconfig(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	for _, marker := range []string{"apiVersion:", "clusters:", "users:"} {
		if !strings.Contains(raw, marker) {
			return false
		}
	}
	// Control characters are what kubectl actually complains about, and they
	// are the signature of a diagnostic or a partial read reaching this file.
	for _, r := range raw {
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return true
}

// WithKubeconfig runs a command with a kubeconfig that exists only for as long
// as the command does.
//
// The alternative - write the file into the workspace, export KUBECONFIG, and
// remember to clean up - leaves a live credential on disk between uses, and
// invites exactly the shortcut of keeping it around because regenerating it is
// mildly annoying. Transience is the safeguard, so the convenient path is the
// one that keeps it: the file is created with 0600 in the OS temp directory,
// never in the repository, and removed on every exit path including a signal.
//
// It returns the command's exit code so the caller can pass it on, because a
// wrapper that swallows a non-zero exit is a wrapper that hides failures.
func WithKubeconfig(ctx *run.Context, argv []string) (int, error) {
	return withRenderedCredential(ctx, argv, "KUBECONFIG", "kubeconfig", WriteKubeconfigTo)
}

// WithTalosconfig runs a command with a talosconfig that exists only for as
// long as the command does.
//
// The same reasoning as WithKubeconfig and, if anything, a stronger case for
// it. A talosconfig authenticates to Talos rather than to Kubernetes - the
// layer below - where it can read machine configuration and reboot or reset a
// node. It is the more dangerous of the two credentials this estate renders,
// so it gets the handling that never leaves it on a disk.
//
// Unlike kubeconfig there is deliberately no form that writes into the
// workspace and returns. That form exists for kubeconfig because tools expect
// a file and somebody has to be able to export KUBECONFIG; it is also the form
// that leaves a live credential lying around waiting for `task clean-secrets`.
// Nothing needs that here, so the only way to use this credential is to hand
// it a command, and its lifetime is that command's.
//
// It exists because the node is the one place in this estate nobody can see.
// Talos has no shell, so every question about a node's own network - whether
// an interface exists, what the extension is doing - is unanswerable without
// it. The alternative reached for instead was a privileged pod with host
// networking, which Pod Security Admission correctly refused.
func WithTalosconfig(ctx *run.Context, argv []string) (int, error) {
	return withRenderedCredential(ctx, argv, "TALOSCONFIG", "talosconfig", writeTalosconfigTo)
}

// withRenderedCredential runs a command with a rendered credential that exists
// only for as long as the command does.
//
// Shared by both credential verbs because the handling *is* the point and must
// not drift between them: one of the two quietly losing the signal handler,
// the 0600, or the removal is exactly the difference nobody notices until a
// credential is sitting on a disk.
//
// It returns the command's exit code so the caller can pass it on, because a
// wrapper that swallows a non-zero exit hides failures.
func withRenderedCredential(
	ctx *run.Context,
	argv []string,
	envVar string,
	label string,
	write func(*run.Context, string) error,
) (int, error) {
	if len(argv) == 0 {
		return 0, errors.New("no command given after --")
	}

	f, err := os.CreateTemp("", label+"-*")
	if err != nil {
		return 0, fmt.Errorf("creating a temporary %s: %w", label, err)
	}
	path := f.Name()
	// Close before writing through the phase, and remove no matter how this
	// returns - including a panic, which would otherwise strand the file.
	f.Close()
	defer os.Remove(path)
	if err := os.Chmod(path, 0o600); err != nil {
		return 0, fmt.Errorf("restricting the temporary %s: %w", label, err)
	}

	// A signal has to remove it too. Without this, Ctrl-C during a long
	// command leaves the credential behind, which is the exact failure this
	// exists to prevent.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	go func() {
		<-sig
		os.Remove(path)
		os.Exit(130)
	}()

	if err := write(ctx, path); err != nil {
		return 0, err
	}

	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	c := exec.Command(argv[0], argv[1:]...)
	c.Env = append(os.Environ(), envVar+"="+path)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = c.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return 0, fmt.Errorf("running %q: %w", argv[0], err)
	}
	return 0, nil
}

// looksLikeTalosconfig is the same shape check as looksLikeKubeconfig, for the
// other credential. A talosconfig is YAML with a selected context and a map of
// them, and shares none of a kubeconfig's markers.
func looksLikeTalosconfig(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	for _, marker := range []string{"context:", "contexts:"} {
		if !strings.Contains(raw, marker) {
			return false
		}
	}
	for _, r := range raw {
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return true
}

func writeTalosconfigTo(ctx *run.Context, dest string) error {
	return writeRenderedCredential(ctx, dest, "talosconfig", looksLikeTalosconfig)
}
