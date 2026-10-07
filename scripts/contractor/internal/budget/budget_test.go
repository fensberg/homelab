package budget

import (
	"strings"
	"testing"

	"homelab/contractor/config"
	"homelab/details/hypervisorapi"
)

const gib = int64(1) << 30

func site(workers int) *config.SiteNetwork {
	s := &config.SiteNetwork{Reserved: []int{10199}}
	for i := range 3 {
		s.Machines = append(s.Machines, config.PlannedMachine{Role: config.ControlPlane, VMID: 10100 + i, Hypervisor: "node0", MemoryBytes: 4 * gib})
		s.Reserved = append(s.Reserved, 10100+i)
	}
	for i := range workers {
		s.Machines = append(s.Machines, config.PlannedMachine{Role: config.Worker, VMID: 10200 + i, Hypervisor: "node0", MemoryBytes: 10 * gib})
		s.Reserved = append(s.Reserved, 10200+i)
	}
	return s
}

// A host of sixty-four gibibytes with a sixteen-gibibyte machine on it that
// is somebody else's, the site's own machines as they stand, and the site's
// template.
func host() map[string]hypervisorapi.Host {
	return map[string]hypervisorapi.Host{"node0": {MemoryBytes: 64 * gib, Machines: []hypervisorapi.Machine{
		{ID: 100, MemoryBytes: 16 * gib},
		{ID: 10100, MemoryBytes: 4 * gib}, {ID: 10200, MemoryBytes: 10 * gib},
		{ID: 10199, MemoryBytes: 4 * gib, Template: true},
		{ID: 900, MemoryBytes: 8 * gib, Template: true},
	}}}
}

// What is left for a site is what the host has, less each thing that is not
// the site's to use: and only those. The site's own machines already on the
// host are what it is asking for, not something taken from it, and a
// template is given memory on paper and never started.
func TestWhatIsLeftForASiteIsTheHostLessWhatIsNotTheSites(t *testing.T) {
	lines, err := Of(host(), site(2))
	if err != nil || len(lines) != 1 {
		t.Fatalf("got %v, %v", lines, err)
	}
	l := lines[0]
	if l.Host != 64*gib || l.Kept != 2*gib || l.Cache != 64*gib/10 || l.Others != 16*gib || l.Reserve != 4*gib {
		t.Errorf("the deductions are %+v; the other machine is 16, the site's own and both templates are none of them", l)
	}
	if want := 64*gib - 2*gib - 64*gib/10 - 16*gib - 4*gib; l.ForTheSite() != want {
		t.Errorf("left for the site: %d, want %d", l.ForTheSite(), want)
	}
	if l.Asked != 3*4*gib+2*10*gib || l.Short() > 0 || Refusal(lines) != nil {
		t.Errorf("three control planes and two workers ask 32 and are left 35.6, and were refused or miscounted: %s", l)
	}
}

func TestASiteThatAsksMoreThanIsLeftIsRefusedWithEveryFigure(t *testing.T) {
	lines, err := Of(host(), site(3))
	if err != nil {
		t.Fatal(err)
	}
	refusal := Refusal(lines)
	if refusal == nil {
		t.Fatalf("42 asked of 35.6 was not refused: %s", lines[0])
	}
	for _, want := range []string{"node0 has 64.0 GiB", "2.0 kept", "6.4 for its cache", "16.0 given to machines that are not this site's", "4.0 held to replace", "35.6 for the site", "ask 42.0", "OVER BY 6.4"} {
		if !strings.Contains(refusal.Error(), want) {
			t.Errorf("the refusal does not carry %q, so that figure cannot be questioned:\n%v", want, refusal)
		}
	}
}

// The cache is a tenth of the host until that is more than sixteen
// gibibytes, which is the hypervisor's own default and no more is gained by
// a larger one than the machines lose.
func TestTheCacheIsATenthOfTheHostAndNeverMoreThanSixteen(t *testing.T) {
	for host, want := range map[int64]int64{32 * gib: 32 * gib / 10, 160 * gib: 16 * gib, 512 * gib: 16 * gib} {
		if got := Cache(host); got != want {
			t.Errorf("a host of %d: cache %d, want %d", host/gib, got, want)
		}
	}
}

// A machine planned onto a hypervisor nobody read cannot be held against
// anything, and is not waved through for that.
func TestAMachineOnAHypervisorNobodyReadIsAnError(t *testing.T) {
	s := site(1)
	s.Machines = append(s.Machines, config.PlannedMachine{Role: config.Worker, VMID: 10209, Hypervisor: "node1", MemoryBytes: 10 * gib})
	if _, err := Of(host(), s); err == nil || !strings.Contains(err.Error(), "node1") {
		t.Errorf("got %v", err)
	}
	if lines, err := Of(host(), &config.SiteNetwork{}); err != nil || len(lines) != 0 {
		t.Errorf("a site that asks for no machines was budgeted as %v, %v", lines, err)
	}
}
