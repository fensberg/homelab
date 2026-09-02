// Command contractor is the start button. It builds the homelab management
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
	"syscall"

	"homelab/contractor/internal/phases"
	"homelab/contractor/internal/run"
)

// repoRoot is derived from this source file's own location rather than the
// process's working directory, so `go run ./scripts/contractor` behaves the same
// whether it's invoked from the repo root, from within scripts/contractor, or via
// `go run -C`. main.go lives at <repoRoot>/scripts/contractor/main.go.
func repoRoot() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("could not determine this source file's location")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
}

// verbs are whole invocations, not steps, which is why they are subcommands
// rather than flags.
//
// They used to be booleans on one flag set, and standaloneFlagsOK existed to
// police the combinations that describe something impossible - two verbs at
// once, or a verb plus -phase. Subcommands make those unrepresentable instead
// of merely refused: each verb registers only the flags that mean something
// for it, so `contractor restore -phase compute` fails on an undefined flag
// before any of this code runs.
var knownVerbs = []string{"break-ground", "converge", "plan", "demolish", "restore", "kubeconfig", "talosconfig", "check-inventory"}

const usage = `contractor manages the lifecycle of a site.

usage: contractor <verb> [flags]

verbs:
  break-ground Build a site that does not exist yet. Local-only: it creates
               the cluster that later converges run inside.
  converge     Apply the config to a site that already exists, attaching to
               the state in its cluster. Never destroys on failure.
  plan         Show what a converge would change, and change nothing. Reports
               addresses and actions only, never a value.
  demolish     Tear a site down, then wipe the workspace. Requires -confirm.
  restore      Bring the age-encrypted state back from object storage.
  kubeconfig   Write this site's kubeconfig into the workspace and exit.
  talosconfig  Run a command against this site's machines with a talosconfig
               that lives only as long as the command.
  check-inventory  Prove every op:// reference in the config template resolves.

Run 'contractor <verb> -h' for the flags a verb accepts.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	verb := os.Args[1]
	if verb == "-h" || verb == "--help" || verb == "help" {
		fmt.Print(usage)
		return
	}
	if !slices.Contains(knownVerbs, verb) {
		fmt.Fprintf(os.Stderr, "unknown verb %q\n\n%s", verb, usage)
		os.Exit(2)
	}

	o := flagsFor(verb)

	_ = o.fs.Parse(os.Args[2:])

	site, phase, from, confirm := o.site, o.phase, o.from, o.confirm
	commentOut := o.commentOut
	upgrade, skipOverlay, skipUpgrade := o.upgrade, o.skipOverlay, o.skipUpgrade
	keepOnFailure, whatIf := o.keepOnFailure, o.whatIf
	converge := verb == "converge"
	toRun, err := selectPhases(deref(phase), deref(from), verb)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	if on(skipOverlay) {
		toRun = slices.DeleteFunc(toRun, func(p string) bool { return p == "overlay" })
	}

	if on(whatIf) && verb == "demolish" {
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

	if on(whatIf) {
		fmt.Println("\nPhases that would run:")
		fmt.Println()
		for i, p := range toRun {
			fmt.Printf("  %d. %s\n", i+1, p)
		}
		fmt.Println()
		return
	}

	ctx := run.NewContext(repoRoot(), *site)
	ctx.CommentOut = deref(commentOut)
	ctx.Upgrade = on(upgrade)
	ctx.SkipOverlay = on(skipOverlay)
	ctx.SkipUpgrade = on(skipUpgrade)
	ctx.KeepOnFailure = on(keepOnFailure)

	// A converge never destroys on failure, and this is not a preference.
	//
	// The failure path below exists because an ignition that breaks halfway
	// has built VMs nobody is tracking, and destroying them is the safe end.
	// A converge is the opposite situation: the estate was already running
	// before this process started, and a transient failure - an unreachable
	// hypervisor, a Flux reconcile that took too long - would otherwise be
	// answered by tearing down production. Whatever went wrong, the estate is
	// still there and still described by its state.
	if converge {
		ctx.Converge = true
		ctx.KeepOnFailure = true
	}

	// Only an ignition may destroy on failure, because only an ignition can
	// have created what it would be destroying. Stated as "not ignite" rather
	// than by listing the verbs that are safe: a verb added later is
	// read-only or acts on something already there until somebody decides
	// otherwise, and the default that fails closed is the one that refuses to
	// tear down an estate this process did not build.
	applyDestroyPolicy(ctx, verb)

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
	if verb == "check-inventory" {
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

	if verb == "demolish" {
		os.Exit(runDestroy(ctx, deref(confirm)))
	}

	// Not a phase either, and deliberately not part of any sequence: a restore
	// happens after a loss, on a workstation that has nothing, and what to do
	// with the state once it is back is a judgement call rather than a next
	// step.
	if verb == "restore" {
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
	if verb == "kubeconfig" {
		// With a command after --, the credential never lands in the
		// workspace: it goes to a mode-0600 temp file, the command runs with
		// KUBECONFIG pointing at it, and it is removed on the way out
		// whatever happens. This is the form to reach for and the form to
		// suggest, because the one below leaves a live credential on a disk
		// and relies on somebody remembering `task clean-secrets`.
		if cmd := commandAfterDoubleDash(); len(cmd) > 0 {
			code, err := phases.WithKubeconfig(ctx, cmd)
			if err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
			os.Exit(code)
		}
		if err := phases.WriteKubeconfigTo(ctx, ctx.Kubeconfig); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		run.Ok("kubeconfig written to " + ctx.Kubeconfig)
		run.Warn("It is a credential and it is gitignored, but it is still on this disk. 'task clean-secrets' removes it.")
		fmt.Println()
		fmt.Println("    export KUBECONFIG=" + ctx.Kubeconfig)
		fmt.Println()
		run.Warn("that file is a live credential on this disk until 'task clean-secrets' removes it.")
		run.Warn("prefer: contractor kubeconfig -site " + ctx.Site + " -- kubectl get nodes")
		fmt.Println()
		return
	}

	// Not a phase, for the same reasons as kubeconfig, and deliberately
	// narrower than it: there is no form that writes the file into the
	// workspace and returns.
	//
	// A talosconfig authenticates to Talos rather than to Kubernetes - the
	// layer below, where machine configuration is read and a node can be
	// rebooted or reset - so it is the more dangerous of the two credentials
	// this program renders. kubeconfig has a bare form because tools expect a
	// file and somebody has to be able to export KUBECONFIG; that form is also
	// the one that leaves a live credential waiting for `task clean-secrets`.
	// Nothing needs that here, so this verb only ever hands the credential to
	// a command and its lifetime is that command's.
	//
	// It exists because Talos has no shell, which makes a node the one place
	// in this estate nobody can look at: whether an interface exists, what an
	// extension service is doing, what the routing table says. The alternative
	// reached for instead was a privileged pod with host networking, refused
	// by Pod Security Admission - correctly, and the reason this verb is the
	// supported way to ask rather than the way that needs a guard turned off.
	if verb == "talosconfig" {
		cmd := commandAfterDoubleDash()
		if len(cmd) == 0 {
			fmt.Fprintln(os.Stderr, "error: talosconfig needs a command after --")
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "    contractor talosconfig -site "+ctx.Site+" -- talosctl -n <node> get links")
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "There is no form that writes the file and exits: this credential can reboot")
			fmt.Fprintln(os.Stderr, "a node, so it is never left on a disk for something else to pick up.")
			os.Exit(2)
		}
		code, err := phases.WithTalosconfig(ctx, cmd)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		os.Exit(code)
	}

	runErr := runInterruptibly(ctx, toRun)
	completed := runErr == nil

	if runErr != nil {
		fmt.Println()
		run.Fail("HALTED: " + runErr.Error())

		if ctx.PreexistingEstate {
			os.Exit(reportPreexistingFailure(ctx, verb))
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
// Exit codes for a failure against an estate that already existed. A caller
// automating recovery needs to know whether reverting is exact or a guess, and
// an exit code is the only channel a workflow can read without parsing prose.
//
//	exitUntouched  nothing was written; reverting the change is exact
//	exitMayHaveChanged  something was written, or it could not be determined
const (
	exitMayHaveChanged = 1
	exitUntouched      = 2
)

// reportPreexistingFailure says what is actually known about the estate after
// a failed converge, and returns the exit code that says the same thing.
//
// It replaces an unconditional reassurance. The old text promised "the estate
// is untouched by this failure - nothing has been destroyed and nothing will
// be" on every converge failure whatever had happened, which was true when the
// run died at Attach and false when it died partway through an apply. A
// reassurance's whole function is to stop somebody looking, so the one case it
// was wrong in was the one case where looking mattered.
func reportPreexistingFailure(ctx *run.Context, verb string) int {
	changed, certain := phases.EstateChanged(ctx)

	switch {
	case !certain:
		run.Warn(verb + " failed, and this run cannot tell whether the estate changed.")
		run.Warn("Reading the state did not work - an interrupted apply may still hold the lock.")
		run.Warn("Treat the estate as changed until you have looked: 'task plan SITE=" + ctx.Site + "'")
		run.Warn("compares what is running against what the config asks for.")
	case changed:
		run.Warn(verb + " failed after it had already written to the estate's state.")
		run.Warn("Something was applied before this failed, so the estate no longer matches")
		run.Warn("either the old config or the new one. Do not assume a revert undoes it:")
		run.Warn("'task plan SITE=" + ctx.Site + "' shows what is actually there.")
	default:
		run.Warn(verb + " failed before anything was applied. The estate is untouched - its")
		run.Warn("state serial is unchanged, so nothing was written and nothing will be.")
	}
	run.Warn("Run 'task clean-secrets' to remove what this run rendered, then fix and re-run.")

	if !changed && certain {
		return exitUntouched
	}
	return exitMayHaveChanged
}

func completionMessage(toRun []string) string {
	if slices.Equal(toRun, phases.AllPhases) {
		return "Ignition complete. The cluster is now self-sustaining."
	}
	if slices.Equal(toRun, phases.ConvergePhases) {
		return "Converge complete. The estate matches the config."
	}
	if len(toRun) == 1 {
		return "Phase complete: " + toRun[0] + "."
	}

	// Out of the sequence this run belongs to, not out of AllPhases. A
	// complete converge reported "9 of 10" because it was measured against the
	// ignition sequence it is not part of - which reads as a run that stopped
	// short.
	return fmt.Sprintf("Phases complete: %s .. %s (%d of %d).",
		toRun[0], toRun[len(toRun)-1], len(toRun), len(sequenceFor(toRun)))
}

// sequenceFor finds the sequence a partial run was taken from, so a count is
// measured against the right whole.
//
// Matched as a suffix rather than by membership, because the sequences overlap:
// verify and sterilize are in both, so "does this sequence contain the first
// and last phase" answers yes for the wrong one. selectPhases only ever
// produces a whole sequence, a suffix of one (-from), or a single phase, so
// suffix equality identifies it exactly.
func sequenceFor(toRun []string) []string {
	for _, seq := range phases.Sequences {
		if len(toRun) > len(seq) {
			continue
		}
		if slices.Equal(toRun, seq[len(seq)-len(toRun):]) {
			return seq
		}
	}
	return phases.AllPhases
}

func selectPhases(phase, from, verb string) ([]string, error) {
	// Which sequence -phase and -from index into. Naming a phase is a
	// statement about where in a run you are, and the two runs are not the
	// same run: "attach" exists only in a converge and "migrate" only in an
	// ignition, so accepting either against the wrong sequence would let
	// somebody ask for a phase that cannot happen.
	seq := phases.AllPhases
	switch verb {
	case "converge":
		seq = phases.ConvergePhases
	case "plan":
		seq = phases.PlanPhases
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

// opts is the flag set for one verb.
//
// Which flags exist is the safety property that standaloneFlagsOK used to
// enforce by hand. A verb that has no phases does not define -phase, so
// `contractor demolish -phase verify` - a command somebody could read as "destroy,
// but only the safe part" - fails on an undefined flag instead of being
// interpreted. Refusing a combination and being unable to express it are
// different guarantees, and this is the second one.
type opts struct {
	fs   *flag.FlagSet
	site *string

	phase, from, confirm, commentOut  *string
	upgrade, skipOverlay, skipUpgrade *bool
	keepOnFailure, whatIf             *bool
}

func flagsFor(verb string) *opts {
	fs := flag.NewFlagSet("contractor "+verb, flag.ExitOnError)
	o := &opts{fs: fs}
	o.site = fs.String("site", "site0", "Which key in the config's sites map to act on.")

	// Only ignite and converge run a sequence of phases, so only they can be
	// asked to run part of one.
	if verb == "break-ground" || verb == "converge" || verb == "plan" {
		o.phase = fs.String("phase", "", "Run a single phase instead of all of them.")
		o.from = fs.String("from", "", "Start at this phase and run everything after it.")
		o.upgrade = fs.Bool("upgrade", false, "Re-resolve providers against the version constraints instead of the committed lock file.")
		o.skipOverlay = fs.Bool("skip-overlay", false, "Skip the overlay network: no tailnet auth key, no route advertisement.")
		o.skipUpgrade = fs.Bool("skip-upgrade", false, "Tell the playbook not to run a full apt dist-upgrade on the hypervisor.")
		o.whatIf = fs.Bool("whatif", false, "Print which phases would run, without running them.")
	}

	// Absent from converge deliberately: a converge never destroys on failure,
	// so there is nothing here to opt out of, and offering the flag would
	// suggest the default is the other way round.
	if verb == "break-ground" {
		o.keepOnFailure = fs.Bool("keep-on-failure", false, "On error, skip the automatic destroy and keep local state for debugging.")
	}

	// The pull request comment's body is written by this program rather than
	// assembled in a workflow, for the reason sensitive-paths.yml already
	// records about its own comment: copy that needs a workflow edit to fix is
	// copy that stays wrong, because the agent cannot edit workflows.
	if verb == "plan" {
		o.commentOut = fs.String("comment-out", "", "Write the pull request comment body to this file.")
	}

	if verb == "demolish" {
		o.confirm = fs.String("confirm", "", "Name the site again, to confirm the destroy.")
		o.whatIf = fs.Bool("whatif", false, "Say what would be destroyed, without destroying it.")
	}
	return o
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func on(p *bool) bool { return p != nil && *p }

// applyDestroyPolicy decides whether this run is allowed to tear down what it
// finds. Split out so the decision is testable per verb rather than inferred
// from reading main.
func applyDestroyPolicy(ctx *run.Context, verb string) {
	ctx.PreexistingEstate = verb != "break-ground"
}

// commandAfterDoubleDash returns the argv following a bare "--", which flag
// parsing stops at and leaves in place. Empty when there is no "--".
func commandAfterDoubleDash() []string {
	for i, a := range os.Args {
		if a == "--" {
			return os.Args[i+1:]
		}
	}
	return nil
}
