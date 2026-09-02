package survey

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const defaultCLI = "tailscale"

// Run is the survey verb.
//
// A surveyor walks the site before anybody builds on it. This one walks the
// overlay: every peer probed rather than asked about, because registration is
// not reachability.
func Run(args []string) int {
	fs := flag.NewFlagSet("survey", flag.ExitOnError)
	var (
		cli     = fs.String("cli", defaultCLI, "Path to the overlay CLI.")
		timeout = fs.Duration("timeout", 5*time.Second, "How long to wait for one peer to answer.")
		budget  = fs.Duration("budget", 2*time.Minute, "Upper bound on the whole survey.")
		expect  = fs.String("expect", "", "Path to the JSON baseline of what the estate declares belongs on the overlay. Without one, survey reports that it was told nothing rather than reporting a clean mesh.")
	)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `survey - does the overlay actually carry traffic

Reports this host's row of the reachability matrix: every peer it can see,
probed rather than asked about. Run it on each overlay member and the rows
assemble into the whole picture - a row of failures against an otherwise
healthy matrix names the broken host without anyone having to reason about it.

Exits non-zero when a peer is registered and does not answer, which is the
condition the vendor's console, the daemon's service status and this estate's
own health gate were all unable to report.

Run it on a member of the overlay. The workstation is not one.

`)
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	ctx, cancel := context.WithTimeout(context.Background(), *budget)
	defer cancel()

	want, err := LoadExpectation(*expect)
	if err != nil {
		fmt.Fprintln(os.Stderr, "survey: "+err.Error())
		return 2
	}

	v, err := run(ctx, *cli, *timeout, want)
	if err != nil {
		fmt.Fprintln(os.Stderr, "survey: "+err.Error())
		return 2
	}

	report(os.Stdout, v)
	if len(v.unreachable()) > 0 || len(v.Findings) > 0 {
		return 1
	}
	return 0
}

func run(ctx context.Context, cli string, per time.Duration, want Expectation) (Verdict, error) {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	raw, err := exec.CommandContext(ctx, cli, "status", "--json").Output()
	if err != nil {
		return Verdict{}, fmt.Errorf("could not read the overlay status. Is this host a member? (%w)", err)
	}

	self, peers, health, err := parseStatus(raw)
	if err != nil {
		return Verdict{}, err
	}

	var results []result
	for _, p := range peers {
		addr := probeAddr(p)
		r := result{Name: displayName(p), IP: addr, Registered: p.Online}
		if addr == "" {
			r.Detail = "no address"
			results = append(results, r)
			continue
		}

		// The probe. This is the whole point of the program: a peer that is
		// registered, tagged, addressed and routed can still carry nothing,
		// and only sending something finds out.
		//
		// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
		out, _ := exec.CommandContext(ctx, cli,
			"ping", "-c", "1", "--timeout", per.String(), addr).CombinedOutput()
		r.Reachable, r.Via, r.Detail = interpretPing(string(out))
		results = append(results, r)
	}
	return Assemble(self, health, results, peers, want), nil
}

func report(w *os.File, v Verdict) {
	fmt.Fprintln(w, "overlay survey")
	if v.From != "" {
		fmt.Fprintf(w, "from %s\n", v.From)
	}
	fmt.Fprintln(w, strings.Repeat("-", 72))

	if len(v.Results) == 0 {
		fmt.Fprintln(w, "no peers visible from here, so this proves nothing about the mesh.")
		fmt.Fprintln(w, strings.Repeat("-", 72))
		return
	}

	for _, r := range v.Results {
		switch {
		case r.Reachable:
			fmt.Fprintf(w, "[ok]   %-28s %-16s %s\n", r.Name, r.IP, r.Via)
		case r.Registered:
			// The finding this program exists for: the console says yes and
			// the wire says no.
			fmt.Fprintf(w, "[FAIL] %-28s %-16s registered but unreachable (%s)\n",
				r.Name, r.IP, r.Detail)
		default:
			fmt.Fprintf(w, "[--]   %-28s %-16s offline, and says so (%s)\n",
				r.Name, r.IP, r.Detail)
		}
	}

	// The daemon's own health notes. On the night this program was written,
	// this was the only place that admitted anything was wrong - and it was
	// in a footnote, not in the status column.
	for _, h := range v.Health {
		fmt.Fprintf(w, "[!!]   %s\n", h)
	}

	// Findings are printed after the row and before the summary, because they
	// are a different kind of problem. A peer that cannot be reached is an
	// outage; a peer nobody declared is somebody else's machine on the mesh.
	for _, f := range v.Findings {
		switch f.Kind {
		case "new-device":
			fmt.Fprintf(w, "[NEW]  %-28s %s\n", f.Name, f.Detail)
		case "no-expectation":
			fmt.Fprintf(w, "[??]   %-28s %s\n", f.Name, f.Detail)
		case "missing-member":
			fmt.Fprintf(w, "[GONE] %-28s %s\n", f.Name, f.Detail)
		default:
			fmt.Fprintf(w, "[??]   %-28s %s\n", f.Name, f.Detail)
		}
	}

	fmt.Fprintln(w, strings.Repeat("-", 72))
	bad := v.unreachable()
	if len(v.Findings) > 0 {
		fmt.Fprintf(w, "%d finding(s) about what is on this mesh, separate from whether it works.\n",
			len(v.Findings))
		fmt.Fprintln(w, "A device nobody declared reaches everything any other device reaches,")
		fmt.Fprintln(w, "so an unrecognised name here is worth a human today rather than a ticket.")
		fmt.Fprintln(w, "Accept a device deliberately by adding it to the baseline, the same way")
		fmt.Fprintln(w, "a sensitive-path change is acknowledged rather than merely observed.")
	}
	if len(bad) == 0 && len(v.Findings) == 0 {
		fmt.Fprintf(w, "%d peer(s) answered. This row of the matrix is clean.\n", len(v.Results))
		return
	}
	if len(bad) == 0 {
		return
	}
	fmt.Fprintf(w, "%d of %d peer(s) are registered and do not answer.\n", len(bad), len(v.Results))
	fmt.Fprintln(w, "Registration is not reachability: a peer can hold a session, appear")
	fmt.Fprintln(w, "online and carry nothing. Run this on the other members too - if they")
	fmt.Fprintln(w, "reach each other and not these, the fault is at the other end.")
}
