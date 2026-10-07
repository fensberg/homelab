package budget

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"homelab/contractor/config"
	"homelab/details/gitenv"
	"homelab/details/hypervisorapi"
	"homelab/details/repopath"
)

const gib = int64(1) << 30

// One test here asks git which files the repository tracks, so git is not
// left to read the machine's own configuration.
func TestMain(m *testing.M) {
	gitenv.Isolate()
	os.Exit(m.Run())
}

func site(workers int) *config.SiteNetwork {
	s := &config.SiteNetwork{Octet: 10, Reserved: []int{10199}}
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

// Tight does not mean seized. A site already over, asking for what it has or
// for less, is told so and not stopped: the machines are there either way,
// and the converge that would shrink them is the one a flat refusal blocks.
// Asking for one gibibyte more than it stands on is refused.
func TestASiteThatIsOverIsStoppedOnlyWhenItAsksForMoreThanItHas(t *testing.T) {
	standing := func(workers int) map[string]hypervisorapi.Host {
		h := host()
		node := h["node0"]
		node.Machines = []hypervisorapi.Machine{{ID: 100, MemoryBytes: 16 * gib}}
		for i := range 3 {
			node.Machines = append(node.Machines, hypervisorapi.Machine{ID: 10100 + i, MemoryBytes: 4 * gib})
		}
		for i := range workers {
			node.Machines = append(node.Machines, hypervisorapi.Machine{ID: 10200 + i, MemoryBytes: 10 * gib})
		}
		h["node0"] = node
		return h
	}
	for name, c := range map[string]struct {
		built, planned int
		refused        bool
		says           string
	}{
		"over, and asking for what it has":      {3, 3, false, "it is not stopped"},
		"over, and asking for less":             {3, 2, false, "fits"},
		"over, and asking for more":             {3, 4, true, "OVER BY"},
		"within, and asking for more than fits": {2, 3, true, "OVER BY"},
	} {
		lines, err := Of(standing(c.built), site(c.planned))
		if err != nil {
			t.Fatal(err)
		}
		if got := Refusal(lines) != nil; got != c.refused {
			t.Errorf("%s: refused = %v, want %v\n%s", name, got, c.refused, lines[0])
		}
		if !strings.Contains(lines[0].String(), c.says) {
			t.Errorf("%s: the line does not say %q:\n%s", name, c.says, lines[0])
		}
	}
}

// The cache is counted here as the host is held to it there.
//
// The budget takes the cache off a host's memory by a rule, and the
// hypervisor's playbook sets the cache's ceiling on the host by the same
// rule, written again in the playbook's own variables. If the two drift the
// budget is counting a cache the host is not held to: smaller than the real
// one and the machines are promised memory the cache is holding, larger and
// a site is refused room it has.
//
// The playbook is found by what it declares, wherever it is.
func TestTheCacheIsCountedAsTheHostIsHeldToIt(t *testing.T) {
	root, err := repopath.Root()
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]*regexp.Regexp{
		"share": regexp.MustCompile(`(?m)^\s*cache_share_of_memory:\s*(\d+)\s*$`),
		"most":  regexp.MustCompile(`(?m)^\s*cache_most_gib:\s*(\d+)\s*$`),
	}
	found := map[string]int64{}
	// Every tracked playbook, asked of git, so nothing that is not the
	// repository's own is read.
	tracked, err := exec.Command("git", "-C", root, "ls-files", "-z", "--", "*.yml", "*.yaml").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range strings.Split(strings.TrimRight(string(tracked), "\x00"), "\x00") {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		for what, pattern := range declared {
			if m := pattern.FindSubmatch(body); m != nil {
				n, _ := strconv.ParseInt(string(m[1]), 10, 64)
				if _, twice := found[what]; twice {
					t.Errorf("the cache's %s is declared in more than one place, and only one can be what the host is held to", what)
				}
				found[what] = n
			}
		}
	}
	if len(found) != len(declared) {
		t.Fatalf("found %v: no playbook declares both what share of a host its cache may take and the most it may take, so nothing holds the host to what is counted here", found)
	}
	if found["share"] != CacheShare || found["most"]*gib != CacheMost {
		t.Errorf("the host is held to a %dth of its memory and at most %d GiB, and the budget counts a %dth and at most %d GiB",
			found["share"], found["most"], int64(CacheShare), CacheMost/gib)
	}
}
