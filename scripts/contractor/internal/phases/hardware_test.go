package phases

import (
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/contractor/config"
	"homelab/contractor/internal/run"
	"homelab/details/hypervisorapi"
	"homelab/details/repopath"
)

// aSiteWithOneHost stands in for asking a site's hypervisors: one host, with
// no display device and one machine that is nobody's here.
func aSiteWithOneHost(*run.Context) (map[string]hypervisorapi.Host, *config.SiteNetwork, error) {
	return map[string]hypervisorapi.Host{"node0": {
			MemoryBytes: 64 << 30, Cores: 16, Sockets: 1, CPUModel: "a processor",
			GPUs:       []hypervisorapi.Device{},
			Datastores: map[string]hypervisorapi.Datastore{"disks": {Type: "zfspool", TotalBytes: 1 << 40}},
			Machines:   []hypervisorapi.Machine{{ID: 105, MemoryBytes: 4 << 30, Cores: 2}},
		}}, &config.SiteNetwork{Machines: []config.PlannedMachine{
			{Role: config.Worker, VMID: 10200, Hypervisor: "node0", MemoryBytes: 10 << 30},
		}, Reserved: []int{10200}}, nil
}

// asked resets what a run remembers of having asked, for the length of a
// test, and counts the askings.
func asked(t *testing.T, answer func(*run.Context) (map[string]hypervisorapi.Host, *config.SiteNetwork, error)) *int {
	t.Helper()
	t.Setenv(hardwareAsked, "")
	t.Setenv("TF_VAR_"+hardwareInput, "")
	before, n := surveySite, 0
	surveySite = func(ctx *run.Context) (map[string]hypervisorapi.Host, *config.SiteNetwork, error) {
		n++
		return answer(ctx)
	}
	t.Cleanup(func() { surveySite = before })
	return &n
}

// What the hypervisors have reaches the cluster root as its variable, whole,
// and they are asked once for a run however many steps reach the root.
func TestTheHardwareIsHandedToTheClusterRootOnceForARun(t *testing.T) {
	n := asked(t, aSiteWithOneHost)
	ctx := run.NewContext(t.TempDir(), "site0")
	for range 3 {
		if _, err := rootFor(ctx, "cluster", nil); err != nil {
			t.Fatal(err)
		}
	}
	if *n != 1 {
		t.Errorf("the hypervisor was asked %d times in one run; its hardware does not change between a converge's steps", *n)
	}
	var handed struct {
		Nodes map[string]hypervisorapi.Host `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(os.Getenv("TF_VAR_"+hardwareInput)), &handed); err != nil {
		t.Fatalf("what was handed over is not what the root's variable takes: %v", err)
	}
	host := handed.Nodes["node0"]
	if host.MemoryBytes != 64<<30 || host.Cores != 16 || host.GPUs == nil || len(host.Machines) != 1 {
		t.Errorf("the host reached the root as %+v", host)
	}
}

// Only what holds still is handed to the root, because the root records it
// and the estate is then held to matching it. A datastore's size moves by
// megabytes a minute as the pool under it fills, so a record that held one
// never matched the host again and every night's record was refused.
func TestWhatIsHandedToTheRootHoldsNoReadingThatMoves(t *testing.T) {
	asked(t, aSiteWithOneHost)
	if _, err := rootFor(run.NewContext(t.TempDir(), "site0"), "cluster", nil); err != nil {
		t.Fatal(err)
	}
	handed := os.Getenv("TF_VAR_" + hardwareInput)
	if strings.Contains(handed, "storages") || strings.Contains(handed, "total_bytes") {
		t.Errorf("a datastore's size was handed to the root, which records it: %s\n\n"+
			"It is a measurement that moves. What is recorded must be the same a minute later, "+
			"or the estate never matches its own record.", handed)
	}
}

// A hypervisor that cannot be asked stops the run before tofu is given
// anything: a size worked out from a host nobody read is a guess.
func TestARunThatCannotReadTheHardwareChangesNothing(t *testing.T) {
	asked(t, func(*run.Context) (map[string]hypervisorapi.Host, *config.SiteNetwork, error) {
		return nil, nil, errors.New("no answer")
	})
	_, err := rootFor(run.NewContext(t.TempDir(), "site0"), "cluster", nil)
	if err == nil || !strings.Contains(err.Error(), "Nothing has been changed") {
		t.Fatalf("a run that could not read the hardware went on, or did not say that nothing changed: %v", err)
	}
	if os.Getenv("TF_VAR_"+hardwareInput) != "" {
		t.Error("the root was handed hardware nobody read")
	}
}

// The real asking, up to the hypervisor's door: the site's nodes from the
// rendered config, the authority from the vault, the token as the header,
// and a hypervisor that does not answer named by its key. The answer itself
// is read by hypervisorapi.Survey, which is tested against a hypervisor that
// does answer.
func TestTheRealAskingFindsTheHypervisorAndSaysWhichOneDidNotAnswer(t *testing.T) {
	ctx := run.NewContext(t.TempDir(), "site0")
	if _, _, err := readHardware(ctx); err == nil {
		t.Error("the hardware was read with no rendered config")
	}
	mustWriteFile(t, ctx.ConfigRendered, `{"sites": {}}`)
	if _, _, err := readHardware(ctx); err == nil || !strings.Contains(err.Error(), "site0") {
		t.Errorf("a config with no such site was read as one with no hypervisors: %v", err)
	}

	// A whole config, with its one hypervisor at an address where nothing
	// is listening, so the asking is refused at once and not after a wait.
	repo, err := repopath.Root()
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(config.CorpusFixture(repo, "valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, ctx.ConfigRendered, strings.ReplaceAll(string(fixture), `"ip": "10.10.0.5"`, `"ip": "127.0.0.1"`))

	// A vault that does not hold the authority: the run is told which phase
	// stores it.
	bin := t.TempDir()
	vault := func(script string) {
		if err := os.WriteFile(filepath.Join(bin, "op"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	vault("exit 1")
	if _, _, err := readHardware(ctx); err == nil || !strings.Contains(err.Error(), "configure-hypervisor") {
		t.Errorf("a vault with no authority did not send the operator to the phase that stores one: %v", err)
	}

	// And one that does, in the form the playbook stores: the asking gets
	// as far as the hypervisor, which is not there.
	stranger := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(stranger.Close)
	authority := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: stranger.Certificate().Raw}))
	vault("echo " + authority)
	_, _, err = readHardware(ctx)
	if err == nil || !strings.Contains(err.Error(), "hypervisor node0") {
		t.Errorf("a hypervisor that does not answer was not named by its key: %v", err)
	}
}

// A site whose machines its hypervisor cannot hold is refused before tofu is
// given anything, and the refusal carries the whole sum.
func TestASiteItsHypervisorCannotHoldIsRefusedWithTheSum(t *testing.T) {
	asked(t, func(ctx *run.Context) (map[string]hypervisorapi.Host, *config.SiteNetwork, error) {
		hosts, site, _ := aSiteWithOneHost(ctx)
		for i := range 6 {
			site.Machines = append(site.Machines, config.PlannedMachine{Role: config.Worker, VMID: 10201 + i, Hypervisor: "node0", MemoryBytes: 10 << 30})
		}
		return hosts, site, nil
	})
	_, err := rootFor(run.NewContext(t.TempDir(), "site0"), "cluster", nil)
	if err == nil {
		t.Fatal("seventy gibibytes of machines were handed to tofu for a host with sixty-four")
	}
	for _, want := range []string{"OVER BY", "node0 has 64.0 GiB", "ask 70.0", "Nothing has been changed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%v", want, err)
		}
	}
	if os.Getenv("TF_VAR_"+hardwareInput) != "" {
		t.Error("the root was handed the hardware of a host the site does not fit on")
	}
}
