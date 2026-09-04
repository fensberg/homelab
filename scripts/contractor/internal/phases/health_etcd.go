package phases

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"homelab/contractor/internal/config"
	"homelab/contractor/internal/run"
)

// The Health phase asks etcd whether its members are well.
//
// Nothing did, before this. The scale-up from three to five control-plane
// nodes passed every check this phase has - nodes Ready and schedulable, every
// DaemonSet at full count, Flux reconciled, the database at its full instance
// count - and not one of them asked etcd a question (#137).
//
// That gap matters most at exactly the moment it was found. Three to five
// moves quorum from 2-of-3 to 3-of-5, which is a gain in fault tolerance only
// if all five are healthy voting members. Two that are registered and unwell
// leave three good members against a quorum of three, so the next single
// failure loses quorum permanently - a cluster that is *less* fault-tolerant
// than the three-node one it replaced, reported as a successful scale-up.
//
// `data.talos_cluster_health` does check etcd, and is worth keeping, but it is
// a gate on the apply rather than a health check: it runs during the apply,
// nothing reads a value from it (its whole job is four depends_on edges), and
// on a converge with no machine changes the read is not deferred and may be
// satisfied from state. So it is not a fresh answer, and its verdict never
// reaches the log. The phase that prints "cluster is healthy" should be able
// to say what it means by it.

// etcdMember is one row of `talosctl etcd members`.
type etcdMember struct {
	Hostname string
	Learner  bool
}

// parseEtcdMembers reads the table `talosctl etcd members` prints.
//
// Parsed rather than asked for as JSON because the command has no structured
// output mode: the table is the interface. That makes the parsing the fragile
// part, which is why it is a pure function with its own tests rather than
// something only a live cluster can exercise - a pattern that silently matched
// nothing would report zero members, and zero members reads exactly like a
// cluster that has not come up yet.
func parseEtcdMembers(out string) ([]etcdMember, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return nil, fmt.Errorf("no member rows in the output:\n%s", out)
	}
	header := lines[0]

	// Located by where the heading starts in the header line, not by counting
	// whitespace-separated fields.
	//
	// The table has two-word headings - PEER URLS and CLIENT URLS - so
	// strings.Fields puts LEARNER at index 7 in the header and the value at
	// index 5 in every row. The first version of this counted fields, matched
	// nothing, and reported "a header and no members" against output that was
	// perfectly well formed. It would have failed the Health phase on a
	// healthy cluster, which is a worse defect than the one this file exists
	// to fix; the test fixture is the real output shape for exactly that
	// reason.
	learnerAt := strings.Index(header, "LEARNER")
	if learnerAt < 0 {
		return nil, fmt.Errorf("no LEARNER column in the output, so whether a member "+
			"votes cannot be determined:\n%s", out)
	}
	hostnameAt := strings.Index(header, "HOSTNAME")

	var members []etcdMember
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		learner, ok := columnAt(line, learnerAt)
		if !ok {
			continue
		}
		m := etcdMember{Learner: strings.EqualFold(learner, "true")}
		if hostnameAt >= 0 {
			if h, ok := columnAt(line, hostnameAt); ok {
				m.Hostname = h
			}
		}
		members = append(members, m)
	}
	if len(members) == 0 {
		return nil, fmt.Errorf("the output had a header and no members:\n%s", out)
	}
	return members, nil
}

// columnAt returns the whitespace-delimited token of a fixed-width row whose
// start is nearest the given column offset.
//
// Nearest rather than exact: a value can be wider or narrower than its
// heading, so the columns drift by a character or two down the table. What
// does not drift is the ordering, so the token starting closest to the
// heading is the one under it.
func columnAt(line string, at int) (string, bool) {
	type tok struct {
		start int
		text  string
	}
	var toks []tok
	i := 0
	for i < len(line) {
		for i < len(line) && line[i] == ' ' {
			i++
		}
		if i >= len(line) {
			break
		}
		start := i
		for i < len(line) && line[i] != ' ' {
			i++
		}
		toks = append(toks, tok{start: start, text: line[start:i]})
	}
	if len(toks) == 0 {
		return "", false
	}

	best, bestDist := "", -1
	for _, t := range toks {
		d := t.start - at
		if d < 0 {
			d = -d
		}
		if bestDist < 0 || d < bestDist {
			best, bestDist = t.text, d
		}
	}
	return best, true
}

// judgeEtcd decides whether the member list is what the config asked for.
//
// Two distinct failures, kept distinct because the remedies differ. Too few
// members means a machine exists that never joined; a learner means one joined
// and is not voting yet, which is normal for a few seconds and a defect after
// that. Both look identical in every other check this phase runs.
func judgeEtcd(members []etcdMember, want int) error {
	var learners []string
	for _, m := range members {
		if m.Learner {
			name := m.Hostname
			if name == "" {
				name = "(unnamed member)"
			}
			learners = append(learners, name)
		}
	}

	switch {
	case len(members) != want:
		return fmt.Errorf(`etcd has %d member(s); the config asks for %d control-plane node(s).

A machine that exists and is Ready but never joined etcd is invisible to every
other check here. If this is a scale-up, quorum has moved without the votes to
back it: five nodes with three members means a quorum of two, and the cluster
is not the more fault-tolerant thing the count implies`, len(members), want)

	case len(learners) > 0:
		return fmt.Errorf(`etcd has %d member(s) still in the learner state: %s.

A learner is replicated to but does not vote. Briefly after a member joins
this is normal; persisting, it means quorum is smaller than the member count
suggests - which is the failure that reads as a successful scale-up in every
other signal`, len(learners), strings.Join(learners, ", "))
	}
	return nil
}

// checkEtcd is the healthCheck the phase runs.
func checkEtcd(ctx *run.Context, _ string) error {
	cfg, err := config.LoadRendered(ctx.ConfigRendered)
	if err != nil {
		return err
	}
	net, err := config.ResolveSiteNetwork(cfg, ctx.Site)
	if err != nil {
		return err
	}
	if len(net.NodeIPs) == 0 {
		return fmt.Errorf("the config describes no control-plane nodes, so there is " +
			"nothing to ask about etcd membership")
	}

	// Asked before anything is attempted, because an absent binary cannot
	// become present by waiting and must not be reported as a sick cluster.
	if _, err := exec.LookPath("talosctl"); err != nil {
		return &Unavailable{
			Tool: "talosctl",
			Why:  "not on PATH, so etcd membership was never measured",
		}
	}

	talosconfig, cleanup, err := writeTalosconfig(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	// Asked of the first node rather than of all of them. Every member sees
	// the same membership list - that is what makes it a cluster - so asking
	// each one would be the same answer several times, and would fail the
	// whole check if one node were merely unreachable.
	out, err := run.CmdOutputEnv(ctx.ClusterDir, []string{"TALOSCONFIG=" + talosconfig},
		"talosctl", "--nodes", net.NodeIPs[0], "etcd", "members")
	if err != nil {
		return fmt.Errorf("could not read etcd membership from the cluster: %w", err)
	}

	members, err := parseEtcdMembers(out)
	if err != nil {
		return err
	}
	return judgeEtcd(members, len(net.NodeIPs))
}

// writeTalosconfig materialises a talosconfig for the life of one check, the
// same shape as writeKubeconfig above it and for the same reason: a rendered
// credential is transient, and its short life is the safeguard rather than an
// inconvenience around it.
func writeTalosconfig(ctx *run.Context) (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", "ignite-talosconfig-*")
	if err != nil {
		return "", func() {}, err
	}
	path = f.Name()
	cleanup = func() { _ = os.Remove(path) }
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := writeTalosconfigTo(ctx, path); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("could not render a talosconfig. Has the Cluster phase run? (%w)", err)
	}
	return path, cleanup, nil
}
