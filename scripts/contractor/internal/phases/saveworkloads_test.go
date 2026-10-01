package phases

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/contractor/config"
	"homelab/contractor/internal/run"
)

// saveFixture is a repository with one workload's declaration, a tofu that
// hands over a kubeconfig, and a kubectl that records what it was asked and
// answers as the test says.
type saveFixture struct {
	ctx *run.Context
	dir string
}

const aDeclaration = `{"what": "its data", "namespace": "apps", "selector": "app=thing",
  "container": "saver", "command": ["/bin/save", "--now"]}`

func newSaveFixture(t *testing.T, declarations map[string]string, pods, readyz, execExit string) *saveFixture {
	t.Helper()
	root := t.TempDir()
	f := &saveFixture{ctx: run.NewContext(root, "site0"), dir: t.TempDir()}
	if err := os.MkdirAll(f.ctx.ClusterDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for workload, body := range declarations {
		path := filepath.Join(root, filepath.FromSlash(config.ApplicationsDir), workload, config.TeardownDeclaration)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(f.dir, "pods"), []byte(pods), 0o600); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(f.dir, "calls")
	scripts := map[string]string{
		"tofu": "printf 'a kubeconfig'\n",
		"kubectl": `echo "$*" >> ` + log + `
case "$1" in
  get)
    case "$*" in
      *readyz*) exit ` + readyz + ` ;;
      *) cat ` + filepath.Join(f.dir, "pods") + ` ;;
    esac ;;
  exec) echo "the workload names what it holds: a-private-name"; exit ` + execExit + ` ;;
esac
`,
	}
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(f.dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", f.dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return f
}

func (f *saveFixture) calls() string {
	b, _ := os.ReadFile(filepath.Join(f.dir, "calls"))
	return string(b)
}

const onePodRunning = `{"items": [
  {"metadata": {"name": "thing-abc"}, "status": {"phase": "Running"}},
  {"metadata": {"name": "thing-old"}, "status": {"phase": "Succeeded"}}]}`

// A declared workload that is running is asked to back up, in the pod and
// container it named, with the command it gave.
func TestARunningWorkloadIsAskedToBackUpAsItDeclared(t *testing.T) {
	f := newSaveFixture(t, map[string]string{"thing": aDeclaration}, onePodRunning, "0", "0")
	if err := SaveWorkloads(f.ctx); err != nil {
		t.Fatal(err)
	}
	calls := f.calls()
	if !strings.Contains(calls, "exec -n apps thing-abc -c saver -- /bin/save --now") {
		t.Errorf("the backup was not run as declared:\n%s", calls)
	}
	if !strings.Contains(calls, "get pods -n apps -l app=thing") {
		t.Errorf("the workload's pods were not looked for by its own selector:\n%s", calls)
	}
	if strings.Contains(calls, "thing-old") {
		t.Errorf("a pod that is not running was asked to back up:\n%s", calls)
	}
}

// A backup that was asked for and failed stops the teardown, and says how to
// see why without printing what the workload printed.
func TestAFailedBackupRefusesTheTeardown(t *testing.T) {
	f := newSaveFixture(t, map[string]string{"thing": aDeclaration}, onePodRunning, "0", "7")
	err := SaveWorkloads(f.ctx)
	if err == nil || errors.Is(err, errClusterUnreachable) {
		t.Fatalf("a failed backup did not refuse the teardown: %v", err)
	}
	for _, want := range []string{"thing", "its data", "Nothing has been destroyed", "kubectl exec -n apps thing-abc -c saver"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "a-private-name") {
		t.Error("the refusal printed the workload's own output, which can name what it holds")
	}
}

// A workload that is not running has nothing a backup lacks, and a repository
// where nothing declares a backup has nothing to ask.
func TestAWorkloadThatIsNotRunningHasNothingToSave(t *testing.T) {
	f := newSaveFixture(t, map[string]string{"thing": aDeclaration}, `{"items": []}`, "0", "0")
	if err := SaveWorkloads(f.ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.calls(), "exec") {
		t.Errorf("a backup was run with no pod to run it in:\n%s", f.calls())
	}

	f = newSaveFixture(t, nil, onePodRunning, "0", "0")
	if err := SaveWorkloads(f.ctx); err != nil {
		t.Fatal(err)
	}
	if f.calls() != "" {
		t.Errorf("the cluster was asked although nothing declares a backup:\n%s", f.calls())
	}
}

// A cluster that does not answer is its own outcome: not a pass, and not the
// refusal a failed backup is, because nothing is running to be asked.
func TestAClusterThatDoesNotAnswerIsNotAFailedBackup(t *testing.T) {
	f := newSaveFixture(t, map[string]string{"thing": aDeclaration}, onePodRunning, "1", "0")
	if err := SaveWorkloads(f.ctx); !errors.Is(err, errClusterUnreachable) {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(f.calls(), "exec") {
		t.Error("a backup was attempted on a cluster that does not answer")
	}
}

// A declaration that cannot be read is a refusal, never a workload with
// nothing to lose, and every workload that declares is asked.
func TestADeclarationThatCannotBeReadIsARefusal(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":             `what: its data`,
		"no namespace":         `{"what": "x", "selector": "a=b", "container": "c", "command": ["s"]}`,
		"no command":           `{"what": "x", "namespace": "n", "selector": "a=b", "container": "c", "command": []}`,
		"a field nobody reads": `{"what": "x", "namespace": "n", "selector": "a=b", "container": "c", "command": ["s"], "extra": ["unread"]}`,
	} {
		f := newSaveFixture(t, map[string]string{"thing": body}, onePodRunning, "0", "0")
		err := SaveWorkloads(f.ctx)
		if err == nil || errors.Is(err, errClusterUnreachable) {
			t.Errorf("%s: read as a workload with nothing to save: %v", name, err)
		}
		if strings.Contains(f.calls(), "exec") {
			t.Errorf("%s: something was run from a declaration that could not be read", name)
		}
	}

	other := strings.ReplaceAll(strings.ReplaceAll(aDeclaration, "apps", "elsewhere"), "saver", "keeper")
	f := newSaveFixture(t, map[string]string{"thing": aDeclaration, "another": other}, onePodRunning, "0", "0")
	if err := SaveWorkloads(f.ctx); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-n apps thing-abc -c saver", "-n elsewhere thing-abc -c keeper"} {
		if !strings.Contains(f.calls(), want) {
			t.Errorf("a declared workload was not asked (%s):\n%s", want, f.calls())
		}
	}
}
