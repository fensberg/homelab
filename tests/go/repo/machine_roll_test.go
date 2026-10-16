package repo

import (
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"homelab/details/applications"
	"homelab/details/capacity"
	"homelab/details/flux"
)

// The machine roll, run for real against a cluster that holds no machines.
//
// WHY. The roll takes a site's machines down one at a time, at night, with
// nobody watching. Every refusal it makes before touching anything is the
// whole of what keeps a bad night from becoming an outage, and none of them
// would be missed by a run that went well: a roll that skipped the etcd check
// rolls a healthy site exactly as before. So each refusal is run against a
// site that should trip it, and the roll is held to touching nothing.
//
// HOW, without an estate. The cluster is Kubernetes' own store and API server
// under kwokctl, the same as the engineer's trial, with no kubelet and no
// controllers: a node is Ready, and on a version, because the test says so.
// talosctl is a stand-in that records what it was asked and, asked to
// upgrade, moves the node's reported version as a machine would. The clock is
// a stand-in for `date`, and GitHub, Prometheus and Alertmanager are one
// local server that answers as each case says. Everything else - the play,
// kubernetes.core's drain, the eviction API, the events - is real.
//
// The play is copied into a scratch tree beside an application of the test's
// own, so what it reads of applications is this test's and not the estate's.
//
// covers: ansible:management/roll/talos-upgrade.yml

// rollMachine is one node as the case sets it up.
type rollMachine struct {
	name, address, version string
	ready, controlPlane    bool
	untrusted              bool
}

// rollCase is a site, and how the outside world answers about it.
type rollCase struct {
	machines     []rollMachine
	want         map[string]string // the machine-image ConfigMap
	runner       string            // the node the job runs on
	hour         string
	etcd         string // what `talosctl etcd members` prints
	instances    int    // the database's, all of them ready
	alerts       string // Prometheus's result array
	jobs         string // the jobs array of the run that passed
	backup       bool   // a workload declares a backup and has a pod
	noApps       bool   // no application's declaration is there to read
	zone         string // the site's timezone in the config template
	upgradeFails bool
}

func baseRollCase() rollCase {
	return rollCase{
		machines: []rollMachine{
			{"cp-100", "10.0.0.100", "v1.13.9", true, true, false},
			{"wk-200", "10.0.0.200", "v1.13.9", true, false, false},
			{"wk-201", "10.0.0.201", "v1.13.9", true, false, false},
			{"wk-202", "10.0.0.202", "v1.13.11", true, false, false},
			{"dmz-100", "10.0.10.100", "v1.13.11", true, false, true},
		},
		want: map[string]string{
			"talos_version":       "v1.13.11",
			"talos_upgrades_from": "v1.13",
			"kubernetes_version":  "1.36.3",
			"installer_image":     "factory.example/installer/abc:v1.13.11",
		},
		runner: "wk-202",
		hour:   "03",
		// Not the estate's own, so a zone written into the play would show.
		zone: "Pacific/Auckland",
		etcd: "NODE         ID                 HOSTNAME   PEER URLS                 CLIENT URLS               LEARNER\n" +
			"10.0.0.100   0123456789abcdef   cp-100     https://10.0.0.100:2380   https://10.0.0.100:2379   false\n",
		instances: 2,
		alerts:    `[]`,
		jobs:      `[{"conclusion": "success", "runner_name": "pr-source-north-abcde"}]`,
	}
}

// rollWorld is what the outside answered and was asked.
type rollWorld struct {
	mu         sync.Mutex
	c          rollCase
	dispatched int
	silenced   int
	unsilenced int
}

func (w *rollWorld) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = io.Copy(io.Discard, r.Body)
	// As the real ones answer: a client that reads a body only when it is
	// said to be JSON must be told so.
	rw.Header().Set("Content-Type", "application/json")
	// GitHub's paths are read by their last parts: the workflow's runs and
	// dispatches, a run, and a run's jobs.
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	last, before := parts[len(parts)-1], ""
	if len(parts) > 1 {
		before = parts[len(parts)-2]
	}
	switch {
	case r.URL.Path == "/api/v1/query":
		fmt.Fprintf(rw, `{"status": "success", "data": {"resultType": "vector", "result": %s}}`, w.c.alerts)
	case r.URL.Path == "/api/v2/silences" && r.Method == http.MethodPost:
		w.silenced++
		fmt.Fprintf(rw, `{"silenceID": "s-%d"}`, w.silenced)
	case before == "silence" && r.Method == http.MethodDelete:
		w.unsilenced++
	case last == "dispatches":
		w.dispatched++
		rw.WriteHeader(http.StatusNoContent)
	case last == "runs" && before != "actions":
		fmt.Fprint(rw, `{"workflow_runs": [{"id": 9}]}`)
	case last == "runs":
		fmt.Fprint(rw, `{"workflow_runs": [{"id": 1}]}`)
	case last == "jobs":
		fmt.Fprintf(rw, `{"jobs": %s}`, w.c.jobs)
	case before == "runs":
		fmt.Fprint(rw, `{"status": "completed", "conclusion": "success", "html_url": "https://example.invalid/run/9"}`)
	default:
		http.NotFound(rw, r)
	}
}

// rollSite is one cluster, reset between cases.
type rollSite struct {
	dir, kubeconfig string
	env             []string
}

// rollKinds are the kinds the roll reads that a bare API server does not
// have: Flux's layers and the database's operator. Each is defined to hold
// anything; its plural is its kind's, as Kubernetes makes them.
var rollKinds = []struct{ group, version, kind string }{
	{"kustomize.toolkit.fluxcd.io", "v1", flux.Kustomization},
	{"helm.toolkit.fluxcd.io", "v2", "HelmRelease"},
	{"postgresql.cnpg.io", "v1", "Cluster"},
}

func resourceOf(group, kind string) string { return strings.ToLower(kind) + "s." + group }

// rollSlots is how many of the roll's tests run at once, each with a
// cluster of its own. Side by side, because each spends most of its time in
// Ansible starting one task after another; no more than this, because each
// holds a store and an API server, and a runner has a few cores.
var rollSlots = make(chan struct{}, 3)

var rollStarting sync.Mutex

func startRollSite(t *testing.T) *rollSite {
	t.Helper()
	heavy(t, "starts a cluster and runs ansible-playbook against it")
	t.Parallel()
	rollSlots <- struct{}{}
	t.Cleanup(func() { <-rollSlots })
	for _, program := range []string{capacity.TrialStarter, capacity.TrialStore, capacity.APIServer, rollAsker, "ansible-playbook"} {
		if _, err := exec.LookPath(program); err != nil {
			t.Fatalf("%s is not on PATH; take delivery of it with scripts/take-delivery.sh", program)
		}
	}
	// One cluster made at a time: the starter picks each program's port as
	// it makes the cluster, and two made at once can pick the same one.
	rollStarting.Lock()
	trial, err := capacity.StartTrial(capacity.APIServer)
	rollStarting.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(trial.Stop)
	s := &rollSite{dir: trial.Dir, kubeconfig: trial.Kubeconfig, env: trial.Env}
	wait := []string{"wait", "--for=condition=Established", "--timeout=60s"}
	for _, k := range rollKinds {
		s.apply(t, fmt.Sprintf(`{"apiVersion": "apiextensions.k8s.io/v1", "kind": "CustomResourceDefinition",
			"metadata": {"name": "%s"},
			"spec": {"group": "%s", "scope": "Namespaced",
				"names": {"plural": "%ss", "singular": "%s", "kind": "%s"},
				"versions": [{"name": "%s", "served": true, "storage": true,
					"schema": {"openAPIV3Schema": {"type": "object", "x-kubernetes-preserve-unknown-fields": true}}}]}}`,
			resourceOf(k.group, k.kind), k.group, strings.ToLower(k.kind), strings.ToLower(k.kind), k.kind, k.version))
		wait = append(wait, "crd/"+resourceOf(k.group, k.kind))
	}
	s.kubectl(t, wait...)
	return s
}

// rollAsker is what the test asks the cluster with.
const rollAsker = "kubectl"

func (s *rollSite) kubectl(t *testing.T, args ...string) string {
	t.Helper()
	out, err := s.try(args...)
	if err != nil {
		t.Fatalf("kubectl %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// try asks the cluster and gives the error rather than failing the test: for
// asking whether something is there.
func (s *rollSite) try(args ...string) (string, error) {
	cmd := exec.Command(rollAsker, args...)
	cmd.Env = append(os.Environ(), s.env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (s *rollSite) apply(t *testing.T, object string) {
	t.Helper()
	f := filepath.Join(s.dir, "object.json")
	if err := os.WriteFile(f, []byte(object), 0o644); err != nil {
		t.Fatal(err)
	}
	s.kubectl(t, "create", "-f", f)
}

// prepare empties the cluster and builds the case's site in it.
func (s *rollSite) prepare(t *testing.T, c rollCase) {
	t.Helper()
	s.kubectl(t, "delete", "nodes", "--all", "--wait=false")
	s.kubectl(t, "delete", "configmap", "machine-image", "-n", "kube-system", "--ignore-not-found")
	s.kubectl(t, "delete", resourceOf(rollKinds[2].group, rollKinds[2].kind), "--all", "-A")
	s.kubectl(t, "delete", "pods", "--all", "-A", "--force", "--grace-period=0")
	s.kubectl(t, "delete", "events", "--all", "-n", "default")

	for _, m := range c.machines {
		labels := map[string]string{capacity.HostnameLabel: m.name}
		if m.controlPlane {
			labels[capacity.ControlPlaneLabel] = ""
		}
		if m.untrusted {
			labels[capacity.UntrustedZone] = "dmz"
		}
		l, err := json.Marshal(labels)
		if err != nil {
			t.Fatal(err)
		}
		s.apply(t, fmt.Sprintf(`{"apiVersion": "v1", "kind": "Node", "metadata": {"name": "%s", "labels": %s}}`, m.name, l))
		ready := "True"
		if !m.ready {
			ready = "False"
		}
		s.kubectl(t, "patch", "node", m.name, "--subresource=status", "--type=merge", "-p",
			fmt.Sprintf(`{"status": {"addresses": [{"type": "InternalIP", "address": "%s"}],
				"conditions": [{"type": "Ready", "status": "%s", "reason": "Given", "message": "given",
					"lastHeartbeatTime": "2026-01-01T00:00:00Z", "lastTransitionTime": "2026-01-01T00:00:00Z"}],
				"nodeInfo": {"osImage": "Talos (%s)", "machineID": "m", "systemUUID": "u", "bootID": "b",
					"kernelVersion": "6", "containerRuntimeVersion": "containerd://2", "kubeletVersion": "v1.36.3",
					"kubeProxyVersion": "", "operatingSystem": "linux", "architecture": "amd64"}}}`, m.address, ready, m.version))
	}
	data, err := json.Marshal(c.want)
	if err != nil {
		t.Fatal(err)
	}
	s.apply(t, fmt.Sprintf(`{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "machine-image", "namespace": "kube-system"}, "data": %s}`, data))
	s.apply(t, fmt.Sprintf(`{"apiVersion": "postgresql.cnpg.io/v1", "kind": "Cluster", "metadata": {"name": "state", "namespace": "default"},
		"spec": {"instances": %d}, "status": {"readyInstances": %d}}`, c.instances, c.instances))
	if c.backup {
		// Made once and kept: a bare API server has no controller to finish
		// deleting a namespace, so one deleted between cases never goes.
		if strings.TrimSpace(s.kubectl(t, "get", "namespace", "given", "--ignore-not-found", "-o", "name")) == "" {
			s.apply(t, `{"apiVersion": "v1", "kind": "Namespace", "metadata": {"name": "given"}}`)
			// Nor one to give it the account every pod is admitted under.
			s.apply(t, `{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": {"name": "default", "namespace": "given"}}`)
		}
		s.apply(t, `{"apiVersion": "v1", "kind": "Pod", "metadata": {"name": "given-0", "namespace": "given", "labels": {"app": "given"}},
			"spec": {"nodeName": "wk-202", "containers": [{"name": "saver", "image": "example.invalid/saver"}]}}`)
		s.kubectl(t, "patch", "pod", "given-0", "-n", "given", "--subresource=status", "--type=merge", "-p",
			`{"status": {"phase": "Running"}}`)
	}
}

// rollResult is what one run of the play did.
type rollResult struct {
	out, output string
	err         error
	talos       []string
	world       *rollWorld
}

// upgraded is each address the roll upgraded, in order.
func (r rollResult) upgraded() []string {
	var out []string
	for _, c := range r.talos {
		if strings.Contains(c, " upgrade ") {
			out = append(out, strings.Fields(c)[3])
		}
	}
	return out
}

func (s *rollSite) roll(t *testing.T, c rollCase) rollResult {
	t.Helper()
	s.prepare(t, c)
	world := &rollWorld{c: c}
	// GitHub, over TLS as the real one is, with a certificate the play is
	// told to trust.
	github := httptest.NewTLSServer(world)
	defer github.Close()
	ca := filepath.Join(t.TempDir(), "github-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: github.Certificate().Raw}), 0o644); err != nil {
		t.Fatal(err)
	}
	// Prometheus and Alertmanager, which the play reaches through the
	// cluster's API server. A bare API server has no network to proxy into,
	// so kubectl is stood in for those requests alone: one naming either
	// service's proxy is answered here, and anything else is the real one's.
	monitoring := httptest.NewServer(world)
	defer monitoring.Close()
	realKubectl, err := exec.LookPath(rollAsker)
	if err != nil {
		t.Fatal(err)
	}

	tree := filepath.Join(t.TempDir(), "tree")
	play := copyRollPlay(t, filepath.Join(tree, "management", "roll"))
	app := filepath.Join(tree, filepath.FromSlash(applications.Dir), "given")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	declaration := `{}`
	if c.backup {
		declaration = `{"before_teardown": {"what": "the data", "selector": "app=given", "container": "saver", "command": ["save", "now"]}}`
	}
	if !c.noApps {
		if err := os.WriteFile(filepath.Join(app, applications.Declaration), []byte(declaration), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// The site's entry in the config template, which is where the play
	// reads the clock the site keeps.
	site := map[string]any{"timezone": c.zone}
	if c.zone == "" {
		site = map[string]any{}
	}
	template, err := json.Marshal(map[string]any{"sites": map[string]any{"north": site}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tree, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "config", "management.tpl.json"), template, 0o644); err != nil {
		t.Fatal(err)
	}

	scratch := t.TempDir()
	bin := filepath.Join(scratch, "bin")
	work := filepath.Join(scratch, "work")
	for _, d := range []string{bin, work} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	calls := filepath.Join(scratch, "talos-calls")
	etcd := filepath.Join(scratch, "etcd-members")
	if err := os.WriteFile(etcd, []byte(c.etcd), 0o644); err != nil {
		t.Fatal(err)
	}
	fails := ""
	if c.upgradeFails {
		fails = "exit 1"
	}
	stubs := map[string]string{
		// The hour, when asked on the site's clock; asked on any other, an
		// hour no window holds.
		"date": "#!/bin/sh\n[ \"$TZ\" = \"" + c.zone + "\" ] && echo " + c.hour + " || echo 12\n",
		// kubectl: a raw request through either monitoring service's proxy
		// goes to this test's server; anything else to the real kubectl.
		"kubectl": `#!/usr/bin/env bash
if [ "$2" = --raw ]; then
  path="$3"
  case "$path" in
    /api/v1/namespaces/monitoring/services/http:kube-prometheus-stack-prometheus:9090/proxy/*) path="${path#*/proxy}" ;;
    /api/v1/namespaces/monitoring/services/http:kube-prometheus-stack-alertmanager:9093/proxy/*) path="${path#*/proxy}" ;;
    *) echo "Error from server (NotFound): $path" >&2; exit 1 ;;
  esac
  case "$1" in
    get) exec curl -fsS "` + monitoring.URL + `$path" ;;
    create) exec curl -fsS -X POST -H 'Content-Type: application/json' --data-binary "@$5" "` + monitoring.URL + `$path" ;;
    delete) exec curl -fsS -X DELETE "` + monitoring.URL + `$path" ;;
  esac
fi
exec ` + realKubectl + ` "$@"
`,
		// talosctl --talosconfig <file> --nodes <address> <command> ...
		"talosctl": `#!/usr/bin/env bash
echo "$*" >>` + calls + `
address="$4"; shift 4
case "$1 ${2:-}" in
  "kubeconfig --force") cp "` + s.kubeconfig + `" "$3" ;;
  "etcd members") cat ` + etcd + ` ;;
  upgrade*)
    ` + fails + `
    image=""; while [ $# -gt 0 ]; do [ "$1" = --image ] && image="$2"; shift; done
    node=$(KUBECONFIG=` + s.kubeconfig + ` kubectl get nodes -o jsonpath="{range .items[?(@.status.addresses[0].address=='$address')]}{.metadata.name}{end}")
    KUBECONFIG=` + s.kubeconfig + ` kubectl patch node "$node" --subresource=status --type=merge \
      -p "{\"status\": {\"nodeInfo\": {\"osImage\": \"Talos (${image##*:})\"}}}" >/dev/null ;;
esac
`,
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(scratch, "github-output")
	access := base64.StdEncoding.EncodeToString([]byte("context: north\ncontexts:\n  north:\n    endpoints: [10.0.0.100]\n    nodes: [10.0.0.100]\n"))

	// Where the roll asks, which is this test's server; and a soak of
	// rounds that do not wait between them, since nothing here changes
	// with time. How many rounds there are is still the play's own.
	cmd := exec.Command("ansible-playbook", "talos-upgrade.yml",
		"-e", "github_host="+strings.TrimPrefix(github.URL, "https://"), "-e", "github_ca="+ca,
		"-e", "soak_round_minutes=0")
	cmd.Dir = play
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"ROLL_WORKDIR="+work,
		"TALOS_ACCESS="+access,
		"RUNNER_NODE_NAME="+c.runner,
		"GITHUB_REPOSITORY=example/estate",
		"GITHUB_TOKEN=stand-in",
		"GITHUB_OUTPUT="+output,
		"SITE=north",
		"ANSIBLE_CONFIG="+filepath.Join(scratch, "none.cfg"),
		"ANSIBLE_LOCAL_TEMP="+filepath.Join(scratch, "ansible"),
		"ANSIBLE_REMOTE_TEMP="+filepath.Join(scratch, "ansible", "remote"),
		"ANSIBLE_NOCOLOR=1",
		"ANSIBLE_LOCALHOST_WARNING=False",
		"ANSIBLE_INVENTORY_UNPARSED_WARNING=False",
	)
	out, err := cmd.CombinedOutput()
	body, _ := os.ReadFile(calls)
	gh, _ := os.ReadFile(output)
	var talos []string
	for _, l := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if l != "" {
			talos = append(talos, l)
		}
	}
	return rollResult{string(out), string(gh), err, talos, world}
}

// copyRollPlay copies the directory the roll's play is in, found by its
// name wherever it is, to dest, and gives dest.
func copyRollPlay(t *testing.T, dest string) string {
	t.Helper()
	root := repoRoot(t)
	var src string
	for _, rel := range tracked(t, func(rel string) bool { return filepath.Base(rel) == "talos-upgrade.yml" }) {
		if src != "" {
			t.Fatalf("two files are called talos-upgrade.yml, so which is the roll is not decided: %s and %s", src, rel)
		}
		src = filepath.Dir(rel)
	}
	if src == "" {
		t.Fatal("no file is called talos-upgrade.yml, so there is no roll to run")
	}
	err := filepath.WalkDir(filepath.Join(root, src), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(filepath.Join(root, src), path)
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, body, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dest
}

// nodeState is each node's version and whether it is cordoned.
func (s *rollSite) nodeState(t *testing.T) map[string]string {
	t.Helper()
	out := s.kubectl(t, "get", "nodes", "-o",
		`jsonpath={range .items[*]}{.metadata.name}={.status.nodeInfo.osImage}/{.spec.unschedulable}{"\n"}{end}`)
	state := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		name, rest, _ := strings.Cut(l, "=")
		state[name] = rest
	}
	return state
}

func TestTheRollMovesEveryMachineInOrderAndHoldsTheSiteToItsHealth(t *testing.T) {
	s := startRollSite(t)
	r := s.roll(t, baseRollCase())
	if r.err != nil {
		t.Fatalf("the roll failed on a site fit to be rolled: %v\n%s", r.err, r.out)
	}
	// Workers before control planes, the first worker the canary; the
	// untrusted worker and the runner's machine are already there.
	if got, want := strings.Join(r.upgraded(), " "), "10.0.0.200 10.0.0.201 10.0.0.100"; got != want {
		t.Errorf("the roll upgraded %q, want %q", got, want)
	}
	var defragged []string
	for _, c := range r.talos {
		if strings.HasSuffix(c, " etcd defrag") {
			defragged = append(defragged, strings.Fields(c)[3])
		}
		if strings.Contains(c, " upgrade ") && !strings.Contains(c, "--image factory.example/installer/abc:v1.13.11") {
			t.Errorf("a machine was upgraded to something other than the cluster's installer image: %s", c)
		}
	}
	if strings.Join(defragged, " ") != "10.0.0.100" {
		t.Errorf("etcd was defragmented on %v, want only the control plane", defragged)
	}
	for name, state := range s.nodeState(t) {
		if state != "Talos (v1.13.11)/" {
			t.Errorf("%s ended as %q, want on v1.13.11 and uncordoned", name, state)
		}
	}
	// The integration tier during the canary's soak, and once at the end.
	if r.world.dispatched != 2 {
		t.Errorf("the integration tier was run %d times, want 2: during the soak and after the roll", r.world.dispatched)
	}
	if r.world.silenced != 6 || r.world.unsilenced != 6 {
		t.Errorf("%d silences were made and %d lifted, want two for each of three machines, all lifted", r.world.silenced, r.world.unsilenced)
	}
	events := s.kubectl(t, "get", "events", "-n", "default", "-o", `jsonpath={range .items[*]}{.involvedObject.name}:{.reason}{"\n"}{end}`)
	for _, m := range []string{"wk-200", "wk-201", "cp-100"} {
		for _, reason := range []string{"MachineRollStarted", "MachineRollFinished"} {
			if !strings.Contains(events, m+":"+reason) {
				t.Errorf("no %s event on %s:\n%s", reason, m, events)
			}
		}
	}
}

func TestTheRollHandsItsOwnMachineToTheNextJob(t *testing.T) {
	s := startRollSite(t)
	c := baseRollCase()
	c.runner = "wk-201"
	r := s.roll(t, c)
	if r.err != nil {
		t.Fatalf("the roll failed: %v\n%s", r.err, r.out)
	}
	if got, want := strings.Join(r.upgraded(), " "), "10.0.0.200 10.0.0.100"; got != want {
		t.Errorf("the roll upgraded %q, want %q and its own machine left to the next job", got, want)
	}
	if state := s.nodeState(t)["wk-201"]; state != "Talos (v1.13.9)/true" {
		t.Errorf("the machine the job ran on ended as %q, want cordoned and not upgraded", state)
	}
	if !strings.Contains(r.output, "handed_over=true") {
		t.Errorf("the run did not say it handed over, so no second job finishes it: %q", r.output)
	}
	if r.world.dispatched != 1 {
		t.Errorf("the integration tier was run %d times, want once, in the soak: the site is not whole until the next job", r.world.dispatched)
	}
}

func TestAMachineThatWillNotUpgradeStopsTheRollAndStaysCordoned(t *testing.T) {
	s := startRollSite(t)
	c := baseRollCase()
	c.upgradeFails = true
	r := s.roll(t, c)
	if r.err == nil {
		t.Fatalf("a machine that would not upgrade did not stop the roll:\n%s", r.out)
	}
	if got := strings.Join(r.upgraded(), " "); got != "10.0.0.200" {
		t.Errorf("the roll tried %q after a machine failed, want it to stop at the first", got)
	}
	if state := s.nodeState(t)["wk-200"]; !strings.HasSuffix(state, "/true") {
		t.Errorf("the machine that failed ended as %q, want it left cordoned", state)
	}
	if !strings.Contains(r.output, "touched=true") || !strings.Contains(r.output, "machine=wk-200") {
		t.Errorf("the run did not say it had touched wk-200, so the workflow would answer it as a refusal: %q", r.output)
	}
}

// Every way a site can be unfit to roll, and the roll touches nothing.
func TestTheRollRefusesASiteNotFitToBeRolledAndTouchesNothing(t *testing.T) {
	s := startRollSite(t)
	for name, tc := range map[string]struct {
		change func(*rollCase)
		says   string
	}{
		"a node not Ready": {func(c *rollCase) { c.machines[2].ready = false }, "Not Ready: wk-201"},
		"a minor version skipped": {func(c *rollCase) {
			for i := range c.machines[:3] {
				c.machines[i].version = "v1.11.4"
			}
		}, "will not move from"},
		"a minor version moved backwards": {func(c *rollCase) { c.machines[1].version = "v1.14.0" }, "will not move from"},
		"a minor version moved without saying so": {func(c *rollCase) {
			c.want["talos_version"], c.want["installer_image"] = "v1.14.0", "factory.example/installer/abc:v1.14.0"
			c.want["talos_upgrades_from"] = "v1.14"
			c.machines[3].version, c.machines[4].version = "v1.14.0", "v1.14.0"
		}, "will not move from"},
		"a bump that says it crosses two minor versions": {func(c *rollCase) {
			c.want["talos_version"], c.want["installer_image"] = "v1.15.0", "factory.example/installer/abc:v1.15.0"
			c.machines[3].version, c.machines[4].version = "v1.15.0", "v1.15.0"
		}, "must be that version's own minor, or the one"},
		"an etcd member that does not vote": {func(c *rollCase) { c.etcd = strings.Replace(c.etcd, "   false\n", "   true\n", 1) }, "not voting"},
		"an etcd member missing": {func(c *rollCase) {
			c.machines = append(c.machines, rollMachine{"cp-101", "10.0.0.101", "v1.13.11", true, true, false})
		}, "for 2 control planes"},
		"a database of one instance":      {func(c *rollCase) { c.instances = 1 }, "fewer than two"},
		"a canary not on the new version": {func(c *rollCase) { c.machines[4].version = "v1.13.9" }, "is not on v1.13.11"},
		"a canary something alerted about": {func(c *rollCase) {
			c.alerts = `[{"metric": {"alertname": "NodeNotReady", "node": "dmz-100"}, "value": [0, "1"]}]`
		}, "An alert has fired for dmz-100"},
		"a canary no job has passed on":                 {func(c *rollCase) { c.jobs = `[{"conclusion": "failure", "runner_name": "pr-source-north-abcde"}]` }, "No pull-request job has passed"},
		"a site that does not say which clock it keeps": {func(c *rollCase) { c.zone = "" }, "has no timezone"},
		"no application's declaration to read":          {func(c *rollCase) { c.noApps = true }, "No application.json was found"},
		"a backup that cannot be taken":                 {func(c *rollCase) { c.backup = true }, "Have every workload take a backup before the first machine goes"},
	} {
		t.Run(name, func(t *testing.T) {
			c := baseRollCase()
			tc.change(&c)
			r := s.roll(t, c)
			if r.err == nil {
				t.Fatalf("the roll went ahead:\n%s", r.out)
			}
			if !strings.Contains(r.out, tc.says) {
				t.Errorf("the refusal does not say %q:\n%s", tc.says, r.out)
			}
			if up := r.upgraded(); len(up) > 0 {
				t.Errorf("machines were upgraded before the refusal: %v", up)
			}
			if strings.Contains(r.output, "touched=true") {
				t.Error("a refusal said it had touched a machine, so the workflow would offer a revert of a roll that never began")
			}
			for node, state := range s.nodeState(t) {
				if strings.HasSuffix(state, "/true") {
					t.Errorf("%s was cordoned before the refusal", node)
				}
			}
		})
	}
}

// A run outside the window, and a run with nothing behind, change nothing
// and are not failures: the timer fires every hour.
func TestTheRollDoesNothingOutsideTheWindowOrWithNothingBehind(t *testing.T) {
	s := startRollSite(t)
	outside := baseRollCase()
	outside.hour = "07"
	r := s.roll(t, outside)
	if r.err != nil || len(r.talos) != 0 {
		t.Errorf("outside the window the roll failed (%v) or asked Talos %v:\n%s", r.err, r.talos, r.out)
	}

	current := baseRollCase()
	for i := range current.machines {
		current.machines[i].version = "v1.13.11"
	}
	r = s.roll(t, current)
	if r.err != nil || len(r.upgraded()) != 0 || r.world.dispatched != 0 {
		t.Errorf("with nothing behind the roll failed (%v), upgraded %v or ran the integration tier %d times:\n%s",
			r.err, r.upgraded(), r.world.dispatched, r.out)
	}
}

// A bump across a minor version that says so moves the machines forward.
func TestARollAcrossAMinorVersionThatSaysSoGoesAhead(t *testing.T) {
	s := startRollSite(t)
	c := baseRollCase()
	c.want["talos_version"], c.want["installer_image"] = "v1.14.0", "factory.example/installer/abc:v1.14.0"
	c.machines[3].version, c.machines[4].version = "v1.14.0", "v1.14.0"
	r := s.roll(t, c)
	if r.err != nil {
		t.Fatalf("a declared minor bump was refused: %v\n%s", r.err, r.out)
	}
	if got := strings.Join(r.upgraded(), " "); got != "10.0.0.200 10.0.0.201 10.0.0.100" {
		t.Errorf("the roll upgraded %q", got)
	}
}
