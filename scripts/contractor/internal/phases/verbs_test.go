package phases

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"homelab/contractor/config"
	"homelab/contractor/internal/run"
	"homelab/contractor/steps"
	"homelab/details/platform"
	"homelab/details/repopath"
)

// A whole verb, run against programs that record what they were asked.
//
// WHY THIS EXISTS. The phases that build, converge and tear down a site were
// each tested a function at a time, with the function's neighbours faked. What
// a verb does - which root each tofu command runs in, in what order, what is
// handed from one root to the next, what is left on disk - was first seen on a
// real estate. Splitting the site into two roots rewrote that sequence, and
// reading it for this test found two faults in it: the orphaned-image check
// still asked the state for the disk image by the hypervisor's hostname, so it
// would have found none and deleted the one the state tracks; and the platform
// root was initialised only in a phase a build can be started after.
//
// So the phases run here exactly as a verb runs them, through Run, with tofu,
// op, age and rclone replaced on PATH by programs that log their arguments.
// Nothing is asserted about the words a phase prints. What is asserted is what
// reaches tofu, and in which root.

// Which phases this runs, and why the rest are not run here. Every phase of
// every verb is in one of the two: a phase nobody has classified fails
// TestEveryPhaseOfEveryVerbIsRunHereOrSaidNotToBe, so a new one cannot join a
// verb without somebody deciding whether this sees it.
var (
	tofuFacing = []string{"overlay", "compute", "cluster", "take-over", "migrate", "backup", "sterilize"}
	notRunHere = map[string]string{
		"render":     "reads the vault and writes the config this starts from",
		"hypervisor": "runs Ansible against the hypervisor, and no tofu",
		"verify":     "probes the hypervisor's network, and no tofu",
		"health":     "asks the cluster through kubectl and talosctl, and no tofu",
		"retire":     "asks the cluster through kubectl and talosctl; retire_test.go runs it against recording programs",
		"plan":       "plans in a copy; TestAPlanWalksTheStepsInACopy runs its sequence",
		"record":     "plans in a copy; TestAPublishableRecordIsSavedWhereAsked runs its sequence",
	}
)

func TestEveryPhaseOfEveryVerbIsRunHereOrSaidNotToBe(t *testing.T) {
	for _, sequence := range Sequences {
		for _, phase := range sequence {
			_, declared := notRunHere[phase]
			if slices.Contains(tofuFacing, phase) == declared {
				t.Errorf("the phase %q is not exactly one of: run by the whole-verb tests, or declared not to be with the reason. A phase added to a verb has to be put in one, so what it asks of tofu is either seen here or known not to be.", phase)
			}
		}
	}
}

// call is one invocation of tofu: the root it ran in, whether the cluster's
// access was in its environment, and its arguments.
type call struct {
	root   string
	access bool
	args   string
}

// verbFixture is a site ready for a verb: both roots, the corpus's valid
// config, and recording programs on PATH.
type verbFixture struct {
	ctx      *run.Context
	dir      string
	waited   []string
	imageFor []string
}

func newVerbFixture(t *testing.T) *verbFixture {
	t.Helper()
	real, err := exec.LookPath("tofu")
	if err != nil {
		t.Skip("tofu is not installed: the address plan is asked through it, so a verb cannot run")
	}
	repo, err := repopath.Root()
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(config.CorpusFixture(repo, "valid.json"))
	if err != nil {
		t.Fatal(err)
	}

	f := &verbFixture{ctx: run.NewContext(t.TempDir(), "site0")}
	for path, body := range map[string]string{f.ctx.ConfigTpl: "{}", f.ctx.ConfigRendered: string(fixture)} {
		mustWriteFile(t, path, body)
	}
	if err := os.MkdirAll(f.ctx.HypervisorDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, root := range f.ctx.Roots() {
		mustWriteFile(t, root.BackendPgOff, "terraform {}\n")
		// What an init leaves, so a phase that asks whether the workspace
		// is initialised is told it is.
		mustWriteFile(t, filepath.Join(root.Dir, ".terraform", "providers", "registry"), "")
	}

	f.dir, _ = fakeTools(t, map[string]string{
		"state":   realisticState,
		"listing": `[{"Path": "latest.tfstate.age"}]`,
		"outputs": clusterOutputs,
		// The state database's connection string, as the platform root
		// outputs it. Put together here rather than written out: a whole
		// connection string in a source file reads as a credential to a
		// scanner, and this one is to an address that does not exist.
		"connstr": fmt.Sprintf("%s://%s:%s@%s:%d/%s", "postgres", "tofu", "stand-in", "192.0.2.1", 30432, "tofu_state"),
	})
	// tofu, as the phases meet it. The address plan is asked through the real
	// one, which answers offline; everything else is logged and answered
	// from the files beside this script.
	tofu := `#!/bin/sh
case "$1" in console) exec ` + real + ` "$@" ;; esac
access=no
[ -n "${TF_VAR_cluster_access:-}" ] && [ -n "${TF_VAR_kubeconfig:-}" ] && access=yes
echo "tofu|$(basename "$PWD")|$access|$*" >> ` + filepath.Join(f.dir, "calls") + `
case "$1 $2" in
  "output -no-color")
    case "$*" in
      *-json*) cat ` + filepath.Join(f.dir, "outputs") + ` ;;
      *state_conn_str*) cat ` + filepath.Join(f.dir, "connstr") + ` ;;
      *) printf 'a-value' ;;
    esac ;;
  "state list") [ -n "$3" ] || printf 'a.tracked\nb.tracked\n' ;;
  "state pull") cat ` + filepath.Join(f.dir, "state") + ` ;;
  "init -input=false")
    case "$*" in *-migrate-state*) [ -f backend_pg.tf ] || : > terraform.tfstate ;; esac ;;
esac
`
	op := "#!/bin/sh\ncase \"$2\" in *ssh_private_key) printf -- '-----BEGIN KEY-----\\nbody\\n-----END KEY-----\\n' ;; *) echo operator ;; esac\n"
	for name, body := range map[string]string{"tofu": tofu, "op": op} {
		if err := os.WriteFile(filepath.Join(f.dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// What a phase sets in the environment, put back when the test ends.
	for _, name := range []string{"PROXMOX_VE_SSH_USERNAME", "PROXMOX_VE_SSH_PRIVATE_KEY", "TF_VAR_cluster_access", "TF_VAR_kubeconfig"} {
		t.Setenv(name, "")
	}
	wasAwait, wasImage := awaitTCP, storedImage
	t.Cleanup(func() { awaitTCP, storedImage = wasAwait, wasImage })
	awaitTCP = func(addr string, _, _ time.Duration) bool {
		f.waited = append(f.waited, addr)
		return true
	}
	storedImage = func(_ config.Hypervisor, node config.Node, _ string) (string, error) {
		f.imageFor = append(f.imageFor, node.Key)
		return "", nil
	}
	return f
}

func mustWriteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// run runs phases in order, as a verb does, and stops at the first failure.
func (f *verbFixture) run(t *testing.T, phases ...string) {
	t.Helper()
	for _, phase := range phases {
		if err := Run(f.ctx, phase); err != nil {
			t.Fatalf("the %s phase: %v", phase, err)
		}
	}
}

// calls is every tofu invocation so far, and every other program's line.
func (f *verbFixture) calls(t *testing.T) (tofu []call, others []string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(f.dir, "calls"))
	if err != nil {
		t.Fatalf("no program was run at all: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 || parts[0] != "tofu" {
			others = append(others, line)
			continue
		}
		tofu = append(tofu, call{root: parts[1], access: parts[2] == "yes", args: parts[3]})
	}
	return tofu, others
}

// applied is the applies of the converge's steps, in the order they ran, as
// "<root>: <targets>". The Overlay phase's own apply, which mints a key and
// is not a step, is left out.
func applied(tofu []call) []string {
	var out []string
	for _, c := range tofu {
		if !strings.HasPrefix(c.args, "apply ") || strings.Contains(c.args, "overlay_key_wanted") {
			continue
		}
		var targets []string
		for _, arg := range strings.Fields(c.args) {
			if t, ok := strings.CutPrefix(arg, "-target="); ok {
				targets = append(targets, t)
			}
		}
		out = append(out, c.root+": "+strings.Join(targets, " "))
	}
	return out
}

// declared is steps.Converge in the same form, for a site with workers.
func declared() []string {
	var out []string
	for _, st := range steps.Converge {
		out = append(out, st.Root+": "+strings.Join(st.Targets, " "))
	}
	return out
}

// first is the index of the first call matching, or -1.
func first(tofu []call, match func(call) bool) int {
	return slices.IndexFunc(tofu, match)
}

// checkTheApplies holds what every verb that applies must do: the declared
// steps, each in its root, in order; a root initialised before anything is
// applied in it; the platform root handed the cluster's access for every
// apply; and nothing run anywhere but in a site's two roots.
func checkTheApplies(t *testing.T, tofu []call) {
	t.Helper()
	if got, want := applied(tofu), declared(); !slices.Equal(got, want) {
		t.Errorf("the applies were:\n  %s\nand the declared steps are:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	for _, c := range tofu {
		if !slices.Contains(config.Roots, c.root) {
			t.Errorf("tofu ran in %q, which is not one of a site's roots: %s", c.root, c.args)
		}
		if c.root == config.PlatformRoot && strings.HasPrefix(c.args, "apply ") && !c.access {
			t.Errorf("the platform root was applied without the cluster's access, so its provider had nothing to configure from: %s", c.args)
		}
	}
	for _, root := range config.Roots {
		init := first(tofu, func(c call) bool { return c.root == root && strings.HasPrefix(c.args, "init ") })
		apply := first(tofu, func(c call) bool {
			return c.root == root && strings.HasPrefix(c.args, "apply ") && !strings.Contains(c.args, "overlay_key_wanted")
		})
		if init < 0 || apply < 0 || init > apply {
			t.Errorf("the %s root was applied (call %d) without being initialised first (call %d)", root, apply, init)
		}
	}
}

// A build, from the first phase that runs tofu to the last.
func TestABuildRunsEachStepInItsRootThenMovesBothStatesAndLeavesNothing(t *testing.T) {
	f := newVerbFixture(t)
	f.run(t, "overlay", "compute", "cluster", "migrate", "backup", "sterilize")
	tofu, others := f.calls(t)
	checkTheApplies(t, tofu)

	// The orphaned-image check asks the state by the node's key, and finding
	// nothing tracked, asks the hypervisor once.
	probe := first(tofu, func(c call) bool { return strings.HasPrefix(c.args, "state list "+steps.DiskImage) })
	if probe < 0 || !strings.Contains(tofu[probe].args, `["node0"]`) {
		t.Errorf("the state was not asked for the disk image by its node's key: %v", tofu)
	}
	if !slices.Equal(f.imageFor, []string{"node0"}) {
		t.Errorf("the hypervisor was asked for a stored image for %v, want node0 once", f.imageFor)
	}
	for _, c := range tofu {
		if strings.Contains(c.args, "fixture-hv0") {
			t.Errorf("a tofu command names the hypervisor by its hostname, which is a vault value: %s", c.args)
		}
	}

	// Both states move into the database, the cluster's first, after the
	// last apply.
	var migrated []string
	lastApply := -1
	for i, c := range tofu {
		if strings.HasPrefix(c.args, "apply ") {
			lastApply = i
		}
		if strings.Contains(c.args, "-migrate-state") {
			migrated = append(migrated, c.root)
			if i < lastApply {
				t.Errorf("the %s root's state was migrated before the last apply", c.root)
			}
		}
	}
	if !slices.Equal(migrated, config.Roots) {
		t.Errorf("states were migrated for %v, want %v in that order", migrated, config.Roots)
	}

	// Both states are backed up, each to its own folder.
	joined := strings.Join(others, "\n")
	for _, root := range config.Roots {
		if first(tofu, func(c call) bool { return c.root == root && c.args == "state pull" }) < 0 {
			t.Errorf("the %s root's state was never pulled, so it is in no backup", root)
		}
		if !strings.Contains(joined, config.StateBackupFolder(root)+"/"+config.LatestStateBackup) {
			t.Errorf("no backup was uploaded to the %s root's folder:\n%s", root, joined)
		}
	}

	// And nothing a run leaves is left, in either root.
	for _, target := range sterilizeTargets(f.ctx) {
		if _, err := os.Stat(target); err == nil {
			t.Errorf("%s survived the run", target)
		}
	}
	if len(f.waited) == 0 {
		t.Error("nothing was waited for, so the machines' and the database's ports were never checked")
	}
}

// A build started at Compute, which is how one is resumed: Overlay does not
// run, and both roots are still initialised before they are applied.
func TestABuildResumedAtComputeStillInitialisesBothRoots(t *testing.T) {
	f := newVerbFixture(t)
	f.run(t, "compute", "cluster")
	tofu, _ := f.calls(t)
	checkTheApplies(t, tofu)
}

// A converge: both roots' states are taken over before anything else is
// asked of tofu, the same steps are applied, and no state is migrated.
func TestAConvergeTakesOverBothRootsAndAppliesTheSameSteps(t *testing.T) {
	f := newVerbFixture(t)
	f.ctx.Converge = true
	f.run(t, "take-over", "compute", "cluster", "backup", "sterilize")
	tofu, _ := f.calls(t)
	checkTheApplies(t, tofu)

	var attached []string
	for i, c := range tofu {
		if strings.Contains(c.args, "-reconfigure") {
			attached = append(attached, c.root)
			if i >= len(config.Roots)*2 {
				t.Errorf("the %s root was attached at call %d, after other work had begun", c.root, i)
			}
		}
		if strings.Contains(c.args, "-migrate-state") {
			t.Errorf("a converge migrated state: %s", c.args)
		}
	}
	if !slices.Equal(attached, config.Roots) {
		t.Errorf("take-over attached %v, want %v", attached, config.Roots)
	}
	if !f.ctx.TakenOverOK {
		t.Error("take-over did not record the state's serial, so a failed converge could not say whether it changed anything")
	}
	for _, target := range sterilizeTargets(f.ctx) {
		if _, err := os.Stat(target); err == nil {
			t.Errorf("%s survived the run", target)
		}
	}
}

// A teardown of a converged site: the cluster root's state comes back out of
// the database and is destroyed, and the platform root is never asked
// anything - what it describes goes with the machines.
func TestATeardownDestroysTheClusterRootAndNeverTouchesThePlatform(t *testing.T) {
	f := newVerbFixture(t)
	for _, root := range f.ctx.Roots() {
		mustWriteFile(t, root.BackendPgOn, "terraform {}\n")
	}
	res := tearDown(f.ctx)
	if !res.Destroyed || !res.SafeToSterilize {
		t.Fatalf("the teardown reported %+v", res)
	}
	tofu, _ := f.calls(t)
	var verbs []string
	for _, c := range tofu {
		if c.root != config.ClusterRoot {
			t.Errorf("the teardown ran tofu in the %s root: %s", c.root, c.args)
		}
		verbs = append(verbs, strings.Fields(c.args)[0])
	}
	demigrate := first(tofu, func(c call) bool { return strings.Contains(c.args, "-migrate-state") })
	destroy := first(tofu, func(c call) bool { return strings.HasPrefix(c.args, "destroy ") })
	if demigrate < 0 || destroy < 0 || demigrate > destroy {
		t.Errorf("the state was not brought out of the database before the destroy: %v", verbs)
	}
	if err := Sterilize(f.ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.ctx.Platform.BackendPgOn); err == nil {
		t.Error("the platform root's backend file survived the teardown, so the next build would dial a database that is gone")
	}
}

// A run against an estate is told which release of the platform to run before
// any tofu command - the one its own line names, by digest, from this
// repository's registry - and a site with no line is refused there.
func TestAVerbNamesTheSitesRelease(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{platform.ReleaseVariable, platform.DigestVariable, platform.CLIConfigVariable} {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
	// Set by whoever started the run, and never an estate's.
	t.Setenv(platform.UnreleasedVariable, "/a/checkout")
	t.Setenv("GITHUB_REPOSITORY", "Example/Estate")
	ctx := run.NewContext(root, "site0")

	if err := NameRelease(ctx); err == nil {
		t.Error("a site with no version to run was run anyway, so it ran whatever was to hand")
	}
	digest := "sha256:" + strings.Repeat("ab", 32)
	mustWriteFile(t, filepath.Join(root, filepath.FromSlash(platform.VersionsFile)), `{"site0": {"platform": "v2026.10.1@`+digest+`"}}`)
	if err := NameRelease(ctx); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		platform.ReleaseVariable: platform.Registry("Example/Estate"),
		platform.DigestVariable:  digest,
	} {
		if got := os.Getenv(name); got != want {
			t.Errorf("%s is %q, want %q", name, got, want)
		}
	}
	if got, set := os.LookupEnv(platform.UnreleasedVariable); set {
		t.Errorf("the run still names a checkout's modules (%q), which its roots would read instead of the release", got)
	}
	// No credential has been written, so tofu is not pointed at one that is
	// not there; once an earlier phase has written it, a later one finds it.
	if got, set := os.LookupEnv(platform.CLIConfigVariable); set {
		t.Errorf("tofu was pointed at settings nothing wrote (%q)", got)
	}
	mustWriteFile(t, ctx.RegistryCredential, "")
	if err := NameRelease(ctx); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(platform.CLIConfigVariable); got != ctx.RegistryCredential {
		t.Errorf("a later phase of the run was pointed at %q, not the credential the first wrote", got)
	}
}

// The credential a root fetches the release with is written for this user
// alone, holds the token as the registry's credential, goes with everything
// else a run renders - and a run with no token stops rather than going on to
// a fetch the registry refuses.
func TestTheRegistryCredentialIsWrittenForTheRunAndRemovedWithIt(t *testing.T) {
	root := t.TempDir()
	t.Setenv(platform.CLIConfigVariable, "")
	os.Unsetenv(platform.CLIConfigVariable)
	ctx := run.NewContext(root, "site0")
	mustWriteFile(t, filepath.Join(root, "config", "keep"), "")
	mustWriteFile(t, filepath.Join(root, filepath.FromSlash(platform.VersionsFile)), `{"site0": {"platform": "v2026.10.1@sha256:`+strings.Repeat("ab", 32)+`"}}`)

	for _, name := range tokenVariables {
		t.Setenv(name, "")
	}
	if err := fetchCredential(ctx); err == nil || !strings.Contains(err.Error(), tokenVariables[0]) {
		t.Errorf("a run with no token went on, or did not say what it wanted: %v", err)
	}
	if _, err := os.Stat(ctx.RegistryCredential); err == nil {
		t.Error("a credential file was written for a run that had no credential")
	}
	// Either name a token arrives under will do.
	t.Setenv(tokenVariables[len(tokenVariables)-1], " a-token\n")
	if err := fetchCredential(ctx); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(platform.CLIConfigVariable); got != ctx.RegistryCredential {
		t.Errorf("tofu is pointed at %q, not the credential", got)
	}
	info, err := os.Stat(ctx.RegistryCredential)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the credential file is %v, readable by more than this user", info.Mode().Perm())
	}
	want, _ := platform.CLIConfig("a-token")
	if got, _ := os.ReadFile(ctx.RegistryCredential); string(got) != string(want) {
		t.Error("the credential file does not hold the token as the registry's credential")
	}
	if err := Sterilize(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ctx.RegistryCredential); err == nil {
		t.Error("the credential file survived Sterilize")
	}
	// And tofu is no longer told of it: told of settings that are not
	// there, it says so on every command, into output a phase reads.
	if got, set := os.LookupEnv(platform.CLIConfigVariable); set {
		t.Errorf("after Sterilize tofu is still pointed at %q, which is gone", got)
	}
}
