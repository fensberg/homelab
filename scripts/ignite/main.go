// Command ignite is the start button. It ignites the homelab management
// cluster from nothing, running nine phases in order:
//
//	1 render     - pull secrets out of 1Password into gitignored files
//	2 overlay    - apply the overlay network policy, mint a hypervisor auth key
//	3 hypervisor - run the Ansible playbook against Proxmox
//	4 verify     - prove the network works BEFORE spending 20 minutes on tofu
//	5 compute    - create the VMs and wait for Talos to answer
//	6 cluster    - apply Talos config, bootstrap etcd, install Flux
//	7 migrate    - move OpenTofu state from this disk into cluster Postgres
//	8 backup     - age-encrypt the state and push it off-site to Cloudflare R2
//	9 sterilize  - wipe every secret and the local state file
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
	flag.Parse()

	if err := destroyFlagsOK(*destroy, *phase, *from); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}

	toRun, err := selectPhases(*phase, *from)
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

	// OpenTofu reads the same config; this tells it which site to use.
	os.Setenv("TF_VAR_site", ctx.Site)

	if *destroy {
		os.Exit(runDestroy(ctx, *confirm))
	}

	runErr := runInterruptibly(ctx, toRun)
	completed := runErr == nil

	if runErr != nil {
		fmt.Println()
		run.Fail("HALTED: " + runErr.Error())

		if ctx.KeepOnFailure {
			run.Warn("-keep-on-failure set: leaving state and secrets in place for debugging.")
			run.Warn("Run 'task clean-secrets' when you are done.")
			os.Exit(1)
		}

		// Only auto-destroy if this run could actually have created
		// infrastructure. A destroy that fails must not be followed by
		// sterilize - see EmergencyDestroy's own comment for why.
		safeToSterilize := true
		if slices.Contains(toRun, "compute") {
			safeToSterilize = phases.EmergencyDestroy(ctx)
		}
		if safeToSterilize {
			_ = phases.Sterilize(ctx, true)
		}
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("Ignition complete. The cluster is now self-sustaining.")
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
func destroyFlagsOK(destroy bool, phase, from string) error {
	if !destroy {
		return nil
	}
	if phase != "" {
		return fmt.Errorf("-destroy and -phase cannot be combined; -destroy is not a phase in the ignition sequence")
	}
	if from != "" {
		return fmt.Errorf("-destroy and -from cannot be combined; -destroy is not a phase in the ignition sequence")
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

func selectPhases(phase, from string) ([]string, error) {
	switch {
	case phase != "":
		if !slices.Contains(phases.AllPhases, phase) {
			return nil, fmt.Errorf("unknown phase '%s'. Valid phases: %v", phase, phases.AllPhases)
		}
		return []string{phase}, nil
	case from != "":
		i := slices.Index(phases.AllPhases, from)
		if i < 0 {
			return nil, fmt.Errorf("unknown phase '%s'. Valid phases: %v", from, phases.AllPhases)
		}
		return slices.Clone(phases.AllPhases[i:]), nil
	default:
		return slices.Clone(phases.AllPhases), nil
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
