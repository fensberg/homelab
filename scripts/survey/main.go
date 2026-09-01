package main

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

func main() {
	var (
		cli     = flag.String("cli", defaultCLI, "Path to the overlay CLI.")
		timeout = flag.Duration("timeout", 5*time.Second, "How long to wait for one peer to answer.")
		budget  = flag.Duration("budget", 2*time.Minute, "Upper bound on the whole survey.")
	)
	flag.Usage = func() {
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
		flag.PrintDefaults()
	}
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *budget)
	defer cancel()

	v, err := run(ctx, *cli, *timeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "survey: "+err.Error())
		os.Exit(2)
	}

	report(os.Stdout, v)
	if len(v.unreachable()) > 0 {
		os.Exit(1)
	}
}

func run(ctx context.Context, cli string, per time.Duration) (Verdict, error) {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	raw, err := exec.CommandContext(ctx, cli, "status", "--json").Output()
	if err != nil {
		return Verdict{}, fmt.Errorf("could not read the overlay status. Is this host a member? (%w)", err)
	}

	self, peers, health, err := parseStatus(raw)
	if err != nil {
		return Verdict{}, err
	}

	v := Verdict{From: self, Health: health}
	for _, p := range peers {
		addr := probeAddr(p)
		r := result{Name: displayName(p), IP: addr, Registered: p.Online}
		if addr == "" {
			r.Detail = "no address"
			v.Results = append(v.Results, r)
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
		v.Results = append(v.Results, r)
	}
	return v, nil
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

	fmt.Fprintln(w, strings.Repeat("-", 72))
	bad := v.unreachable()
	if len(bad) == 0 {
		fmt.Fprintf(w, "%d peer(s) answered. This row of the matrix is clean.\n", len(v.Results))
		return
	}
	fmt.Fprintf(w, "%d of %d peer(s) are registered and do not answer.\n", len(bad), len(v.Results))
	fmt.Fprintln(w, "Registration is not reachability: a peer can hold a session, appear")
	fmt.Fprintln(w, "online and carry nothing. Run this on the other members too - if they")
	fmt.Fprintln(w, "reach each other and not these, the fault is at the other end.")
}
