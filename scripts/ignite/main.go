// Command ignite is the start button. It ignites the homelab management
// cluster from nothing, running ten phases in order:
//
//	 1 render     - pull secrets out of 1Password into gitignored files
//	 2 overlay    - apply the overlay network policy, mint a hypervisor auth key
//	 3 hypervisor - run the Ansible playbook against Proxmox
//	 4 verify     - prove the network works BEFORE spending 20 minutes on tofu
//	 5 compute    - create the VMs and wait for Talos to answer
//	 6 cluster    - apply Talos config, bootstrap etcd, install Flux
//	 7 health     - refuse to go on unless the cluster actually converged
//	 8 migrate    - move OpenTofu state from this disk into cluster Postgres
//	 9 backup     - age-encrypt the state and push it off-site to Cloudflare R2
//	10 sterilize  - wipe every secret and the local state file
//
// Safety model: the workspace is always sterilized on the way out. What
// differs is what happens to infrastructure if the run does not reach the
// end.
//
//	Success           -> state lives in Postgres and R2, local copy deleted
//	Failure           -> `tofu destroy` runs FIRST, then local state is
//	                     deleted, so nothing is left orphaned in Proxmox
//	-keep-on-failure  -> stop and keep state so you can debug. You are then
//	                     responsible for cleaning up.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"

	"homelab/ignite/internal/phases"
	"homelab/ignite/internal/run"
)

// repoRoot is derived from this source file's own location rather than the
// process's working directory, so `go run ./scripts/ignite` behaves the same
// whether it's invoked from the repo root, from within scripts/ignite, or via
// `go run -C`. main.go lives at <repoRoot>/scripts/ignite/main.go.
func repoRoot() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("could not determine this source file's location")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
}

func main() {
	site := flag.String("site", "site0", "Which key in the config's sites map to deploy.")
	phase := flag.String("phase", "", "Run a single phase instead of all of them.")
	from := flag.String("from", "", "Start at this phase and run everything after it.")
	upgrade := flag.Bool("upgrade", false, "Re-resolve providers against the version constraints instead of the committed lock file.")
	skipOverlay := flag.Bool("skip-overlay", false, "Skip the overlay network: no tailnet auth key, no route advertisement.")
	skipUpgrade := flag.Bool("skip-upgrade", false, "Tell the playbook not to run a full apt dist-upgrade on the hypervisor.")
	keepOnFailure := flag.Bool("keep-on-failure", false, "On error, skip the automatic destroy and keep local state for debugging.")
	whatIf := flag.Bool("whatif", false, "Print which phases would run, without running them.")
	destroy := flag.Bool("destroy", false, "Tear this site's infrastructure down, then wipe the workspace. Requires -confirm.")
	confirm := flag.String("confirm", "", "Name the site again, to confirm -destroy.")
	restore := flag.Bool("restore", false, "Bring the age-encrypted state back from object storage. Refuses if local state already exists.")
	kubeconfig := flag.Bool("kubeconfig", false, "Write this site's kubeconfig into the workspace and exit. It is a credential; 'task clean-secrets' removes it.")
	converge := flag.Bool("converge", false, "Apply the config to an estate that already exists, attaching to its state instead of building from scratch.")
	checkVault := flag.Bool("check-vault", false, "Prove every op:// reference in the config template resolves. Reports structure only - never a value.")
	flag.Parse()

	if err := standaloneFlagsOK(modes{Destroy: *destroy, Restore: *restore, Kubeconfig: *kubeconfig, CheckVault: *checkVault}, *phase, *from); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}

	toRun, err := selectPhases(*phase, *from, *converge)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	if *skipOverlay {
		toRun = slices.DeleteFunc(toRun, func(p string) bool { return p == "overlay" })
	}

	if *whatIf && *destroy {
		fmt.Printf(`
Would destroy site %q:

  1. render     - pull secrets from 1Password (this is the credential check)
  2. teardown   - migrate state out of the cluster if it lives there, then
                  'tofu destroy'
  3. sterilize  - wipe every secret and the local state file

Nothing has been touched. Re-run without -whatif to do it.

`, *site)
		return
	}

	if *whatIf {
		fmt.Println("\nPhases that would run:")
		fmt.Println()
		for i, p := range toRun {
			fmt.Printf("  %d. %s\n", i+1, p)
		}
		fmt.Println()
		return
	}

	ctx := run.NewContext(repoRoot(), *site)
	ctx.Upgrade = *upgrade
	ctx.SkipOverlay = *skipOverlay
	ctx.SkipUpgrade = *skipUpgrade
	ctx.KeepOnFailure = *keepOnFailure

	// A converge never destroys on failure, and this is not a preference.
	//
	// The failure path below exists because an ignition that breaks halfway
	// has built VMs nobody is tracking, and destroying them is the safe end.
	// A converge is the opposite situation: the estate was already running
	// before this process started, and a transient failure - an unreachable
	// hypervisor, a Flux reconcile that took too long - would otherwise be
	// answered by tearing down production. Whatever went wrong, the estate is
	// still there and still described by its state.
	if *converge {
		ctx.Converge = true
		ctx.KeepOnFailure = true
	}

	// OpenTofu reads the same config; this tells it which site to use.
	os.Setenv("TF_VAR_site", ctx.Site)

	// Deliberately ahead of EnsureStateEncryption, which is otherwise the
	// first thing to run. That function reads the encryption passphrase out of
	// the vault, so a vault this check exists to diagnose would fail there
	// first - and the operator would be told the state key is unreachable
	// rather than which reference is wrong. A diagnostic that cannot run when
	// its subject is broken is not a diagnostic.
	//
	// It writes nothing and reads no values, so it is also the one mode with
	// nothing to sterilize afterwards.
	if *checkVault {
		if err := phases.CheckVault(ctx); err != nil {
			fmt.Println()
			run.Fail("HALTED: " + err.Error())
			os.Exit(1)
		}
		return
	}

	// Before any phase, including -destroy: state is encrypted at rest, so a
	// tofu invocation without TF_ENCRYPTION cannot read it. Setting it per
	// phase would leave `-from cluster` and the teardown unable to reach the
	// state they exist to operate on.
	if err := phases.EnsureStateEncryption(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	if *destroy {
		os.Exit(runDestroy(ctx, *confirm))
	}

	// Not a phase either, and deliberately not part of any sequence: a restore
	// happens after a loss, on a workstation that has nothing, and what to do
	// with the state once it is back is a judgement call rather than a next
	// step.
	if *restore {
		if err := phases.Restore(ctx); err != nil {
			fmt.Println()
			run.Fail("HALTED: " + err.Error())
			os.Exit(1)
		}
		return
	}

	// Not a phase: it creates nothing, waits for nothing and has no place in
	// the ignition sequence. It exists because looking at the cluster used to
	// mean pasting a tofu output through a shell pipeline and remembering to
	// delete the result.
	if *kubeconfig {
		if err := phases.WriteKubeconfigTo(ctx, ctx.Kubeconfig); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println()
		fmt.Println("    export KUBECONFIG=" + ctx.Kubeconfig)
		fmt.Println()
		return
	}

	runErr := runInterruptibly(ctx, toRun)
	completed := runErr == nil

	if runErr != nil {
		fmt.Println()
		run.Fail("HALTED: " + runErr.Error())

		if ctx.Converge {
			run.Warn("converge failed. The estate is untouched by this failure - it was running before this")
			run.Warn("started and its state still describes it. Nothing has been destroyed and nothing will be.")
			run.Warn("Run 'task clean-secrets' to remove what this run rendered, then fix and re-run.")
			os.Exit(1)
		}

		if ctx.KeepOnFailure {
			run.Warn("-keep-on-failure set: leaving state and secrets in place for debugging.")
			run.Warn("Run 'task clean-secrets' when you are done.")
			os.Exit(1)
		}

		// "Could THIS RUN have created infrastructure?" is the wrong
		// question, and asking it orphaned a real estate: a `-from cluster`
		// run failed, its phase list contained no "compute", so the destroy
		// was skipped - and sterilize then deleted the state file describing
		// three VMs and a template that an earlier `-phase compute` had
		// created and left running.
		//
		// The right question is whether any state exists, which is a fact
		// about the workspace rather than about this invocation.
		// EmergencyDestroy already establishes that itself: it reports
		// "nothing for tofu to destroy" and returns true when there is no
		// state, so calling it unconditionally is both simpler and correct.
		safeToSterilize := phases.EmergencyDestroy(ctx)
		if safeToSterilize {
			_ = phases.Sterilize(ctx, true)
		}
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println(completionMessage(toRun))
	fmt.Println()

	// Belt and braces: on a successful full run the Sterilize phase has
	// already cleaned up, but a partial run (a single -phase, or -from
	// stopping short of sterilize) may leave secrets on disk. Never do
	// that, unless the operator explicitly asked to keep them.
	if completed && !slices.Contains(toRun, "sterilize") && !ctx.KeepOnFailure {
		_ = phases.Sterilize(ctx, true)
	}
}

// destroyFlagsOK rejects flag combinations that cannot mean anything.
// -destroy is not a phase and does not compose with the phase selectors: a
// command that appeared to say "destroy, but only the verify phase" would be
// read by somebody as a safe thing to run.
// modes are the invocations that are not the ignition sequence. Each reaches
// real infrastructure or real credentials on its own terms, and none of them
// is a step that composes with the others.
type modes struct {
	Destroy    bool
	Restore    bool
	Kubeconfig bool
	CheckVault bool
}

func (m modes) named() []string {
	var out []string
	for _, c := range []struct {
		on   bool
		flag string
	}{{m.Destroy, "-destroy"}, {m.Restore, "-restore"}, {m.Kubeconfig, "-kubeconfig"}, {m.CheckVault, "-check-vault"}} {
		if c.on {
			out = append(out, c.flag)
		}
	}
	return out
}

// standaloneFlagsOK refuses the combinations that describe something which does
// not exist.
//
// Two rules, and the second is the one that matters: -destroy and -restore
// both reach real infrastructure, and the order they would run in if both were
// passed is not something anybody should have to guess at.
func standaloneFlagsOK(m modes, phase, from string) error {
	on := m.named()
	if len(on) == 0 {
		return nil
	}
	if len(on) > 1 {
		return fmt.Errorf("%s cannot be combined; each is a whole invocation, not a step", strings.Join(on, " and "))
	}
	if phase != "" {
		return fmt.Errorf("%s and -phase cannot be combined; %s is not a phase in the ignition sequence", on[0], on[0])
	}
	if from != "" {
		return fmt.Errorf("%s and -from cannot be combined; %s is not a phase in the ignition sequence", on[0], on[0])
	}
	return nil
}

// runDestroy runs a deliberate teardown and returns the process exit code.
//
// Interrupt handling here is deliberately NOT the ignition path's. A normal
// run answers Ctrl-C with destroy-then-sterilize, because an interrupted
// build may have left infrastructure nothing is tracking. Doing that to an
// interrupted *destroy* would be catastrophic in the exact way this program
// already learned once: sterilize would wipe the state describing whatever
// the interrupted destroy had not reached yet, leaving VMs running that
// nothing can find. So this waits for the in-flight tofu to exit and release
// its lock, then stops - state and secrets intact, ready to be re-run.
func runDestroy(ctx *run.Context, confirm string) int {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	resultCh := make(chan error, 1)
	go func() { resultCh <- phases.Destroy(ctx, confirm) }()

	var err error
	select {
	case err = <-resultCh:
	case sig := <-sigCh:
		fmt.Println()
		run.Warn(fmt.Sprintf("received %s - waiting for the current step to exit", sig))
		<-resultCh
		run.Warn("Destroy was interrupted. State and secrets have been left exactly as they are,")
		run.Warn("on purpose: wiping them now is how VMs get orphaned. Re-run the same command.")
		return 1
	}

	if err != nil {
		fmt.Println()
		run.Fail("HALTED: " + err.Error())
		return 1
	}

	fmt.Println()
	fmt.Println("Site destroyed and workspace sterilized.")
	fmt.Println()
	return 0
}

// completionMessage says what actually finished.
//
// It used to say "Ignition complete. The cluster is now self-sustaining."
// after any successful invocation - including `-phase render`, which decrypts
// a JSON file and touches no infrastructure at all. A summary that overstates
// what happened is not merely untidy; it is the line that stops somebody
// looking, which is the same way "Site destroyed" came to be printed over
// three running VMs.
//
// The full claim requires the full sequence, starting at the first phase. A
// run beginning at `-from migrate` reaches the end without ever having built
// the cluster it would be describing.
func completionMessage(toRun []string) string {
	if len(toRun) == len(phases.AllPhases) && slices.Equal(toRun, phases.AllPhases) {
		return "Ignition complete. The cluster is now self-sustaining."
	}
	if len(toRun) == 1 {
		return "Phase complete: " + toRun[0] + "."
	}
	return fmt.Sprintf("Phases complete: %s .. %s (%d of %d).",
		toRun[0], toRun[len(toRun)-1], len(toRun), len(phases.AllPhases))
}

func selectPhases(phase, from string, converge bool) ([]string, error) {
	// Which sequence -phase and -from index into. Naming a phase is a
	// statement about where in a run you are, and the two runs are not the
	// same run: "attach" exists only in a converge and "migrate" only in an
	// ignition, so accepting either against the wrong sequence would let
	// somebody ask for a phase that cannot happen.
	seq := phases.AllPhases
	if converge {
		seq = phases.ConvergePhases
	}

	switch {
	case phase != "":
		if !slices.Contains(seq, phase) {
			return nil, fmt.Errorf("unknown phase '%s'. Valid phases: %v", phase, seq)
		}
		return []string{phase}, nil
	case from != "":
		i := slices.Index(seq, from)
		if i < 0 {
			return nil, fmt.Errorf("unknown phase '%s'. Valid phases: %v", from, seq)
		}
		return slices.Clone(seq[i:]), nil
	default:
		return slices.Clone(seq), nil
	}
}

func runPhases(ctx *run.Context, toRun []string) error {
	for _, p := range toRun {
		if err := phases.Run(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

// runInterruptibly runs the phase sequence in the background and races it
// against Ctrl-C / SIGTERM, so an interrupted run gets the same
// destroy-then-sterilize cleanup a normal failure does.
//
// Without this, Ctrl-C's default action kills the process immediately -
// no signal handler means no chance to run cleanup at all. That is exactly
// what "the workspace is sterilized on every exit" is supposed to prevent,
// and an interrupted run is not a hypothetical: a hung phase is the most
// likely moment anyone actually reaches for Ctrl-C.
//
// The in-flight external command (tofu/ansible-playbook/ssh) receives the
// same signal directly, since it shares this process's terminal foreground
// group. It is not necessarily dead by the time the select below wakes up -
// tofu in particular runs its own graceful shutdown ("Gracefully shutting
// down... Stopping operation...") that takes real time to actually release
// its state lock. Proceeding straight to EmergencyDestroy's own `tofu
// destroy` without waiting for that was tried and failed for real: it hit
// "Error acquiring the state lock" because the interrupted apply still held
// it, destroy aborted, and sterilize still ran afterward regardless - wiping
// the only state that could have destroyed the three VMs it left running.
// Waiting for resultCh a second time blocks until that original subprocess
// has actually exited and released its lock, so cleanup only ever starts
// once it is genuinely safe to.
func runInterruptibly(ctx *run.Context, toRun []string) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	resultCh := make(chan error, 1)
	go func() { resultCh <- runPhases(ctx, toRun) }()

	select {
	case err := <-resultCh:
		return err
	case sig := <-sigCh:
		fmt.Println()
		run.Warn(fmt.Sprintf("received %s - waiting for the current step to exit before cleaning up", sig))
		<-resultCh
		return fmt.Errorf("interrupted by %s", sig)
	}
}
