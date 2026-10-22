// The lock half of guard-deliveries: is every delivery that has no lockfile of
// its own ecosystem installed from scripts/deliveries.lock, and does the lock
// say what the declarations say?
//
// SECURITY CHECKS; PROCUREMENT ORDERS. `procurement order` writes the lock from
// the versions in scripts/versions.env and the pypi: and fetch: fields on the
// tools: entries in scripts/approved-suppliers.yml. This reads the same two
// files and refuses a lock that disagrees, and never writes one. Both programs
// read the declarations - separate modules, no shared code, by design - and
// `task order-deliveries` runs this straight after ordering, so a reading that
// drifts between them fails there rather than in CI.
//
// OFFLINE AND FAST, because it runs on every commit and before the Format lane
// installs anything. It cannot tell whether a hash is the right hash - only
// ordering again can - but it can tell that the lock covers every declared
// delivery at the pinned version, covers nothing else, and gives the installer
// a hash for every line, which is the difference between a lock and a list.
package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	thelock "homelab/details/deliveries"
	"homelab/details/envfile"
	"homelab/details/suppliers"
)

const (
	lockPath     = thelock.Path
	versionsPath = "scripts/versions.env"
)

// lockedDelivery is one tools: entry that says how it is delivered.
type lockedDelivery struct {
	Kind   string
	Name   string
	Source string
	Spec   string
	// The file an archive gives up, when it is not named for the repository
	// it comes from - fluxcd/flux2 ships a binary called flux. Empty means
	// the last element of the source, which is right for every other tool.
	Binary     string
	VersionKey string
	Version    string
}

var pep503 = regexp.MustCompile(`[-_.]+`)

// deliveryKinds are the fields a tools: entry may declare, each with how the
// section's name is derived. The same derivation procurement writes with; a
// difference between the two is a lock this refuses the moment it is ordered.
var deliveryKinds = map[string]func(source, spec string) string{
	"pypi":  func(_, spec string) string { return strings.ToLower(pep503.ReplaceAllString(spec, "-")) },
	"fetch": func(source, _ string) string { return source[strings.LastIndex(source, "/")+1:] },
}

var (
	lockHash = regexp.MustCompile(` --hash=sha256:[0-9a-f]{64}(\s|$)`)
)

// declaredDeliveries reads every tools: entry that declares a kind, with its
// version from versions.env.
func declaredDeliveries(root string) ([]lockedDelivery, error) {
	suppliers, err := os.ReadFile(filepath.Join(root, suppliersPath))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", suppliersPath, err)
	}
	versions, err := os.ReadFile(filepath.Join(root, versionsPath))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", versionsPath, err)
	}
	deliveries, err := deliveriesIn(string(suppliers), envfile.Parse(string(versions)))
	if err != nil {
		return nil, err
	}
	if len(deliveries) == 0 {
		// Not a pass. This estate installs tools that are delivered this way, so
		// finding none means the read has stopped matching the file - and a
		// guard reporting clean over a list it could not read is a blind spot.
		return nil, errors.New("no tools: entry in " + suppliersPath + " declares pypi: or fetch:, " +
			"so every delivery would pass unchecked")
	}
	return deliveries, nil
}

// deliveriesIn reads the tools: entries that declare a kind.
func deliveriesIn(listed string, versions map[string]string) ([]lockedDelivery, error) {
	var out []lockedDelivery
	var cur *lockedDelivery
	flush := func() error {
		defer func() { cur = nil }()
		if cur == nil || cur.Kind == "" {
			return nil
		}
		if cur.VersionKey == "" {
			return fmt.Errorf("the tools: entry for %s declares %s: and no version:", cur.Source, cur.Kind)
		}
		v, ok := versions[cur.VersionKey]
		if !ok || v == "" {
			return fmt.Errorf("the tools: entry for %s reads its version from %s, which %s does not set", cur.Source, cur.VersionKey, versionsPath)
		}
		cur.Version = v
		cur.Name = deliveryKinds[cur.Kind](cur.Source, cur.Spec)
		if cur.Binary != "" {
			cur.Name = cur.Binary
		}
		out = append(out, *cur)
		return nil
	}
	tools, err := suppliers.Tools([]byte(listed))
	if err != nil {
		return nil, err
	}
	for _, tool := range tools {
		if err := flush(); err != nil {
			return nil, err
		}
		cur = &lockedDelivery{Source: tool.Source, VersionKey: tool.Fields["version"], Binary: tool.Fields["binary"]}
		// In the order of their names, so that which of two is called the
		// second does not change from one run to the next.
		kinds := make([]string, 0, len(deliveryKinds))
		for kind := range deliveryKinds {
			kinds = append(kinds, kind)
		}
		sort.Strings(kinds)
		for _, kind := range kinds {
			spec, declared := tool.Fields[kind]
			if !declared {
				continue
			}
			if cur.Kind != "" {
				return nil, fmt.Errorf("the tools: entry for %s declares both %s: and %s:", cur.Source, cur.Kind, kind)
			}
			cur.Kind, cur.Spec = kind, spec
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return out, nil
}

// checkDeliveriesLock compares the lock with what it must have been made from.
func checkDeliveriesLock(root string) ([]string, error) {
	deliveries, err := declaredDeliveries(root)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(filepath.Join(root, lockPath))
	if errors.Is(err, os.ErrNotExist) {
		return []string{lockPath + " does not exist, so nothing is installed by hash"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", lockPath, err)
	}
	sections, err := thelock.Parse(string(body))
	if err != nil {
		return []string{lockPath + " cannot be read: " + err.Error()}, nil
	}

	var findings []string
	declared := map[string]bool{}
	names := map[string]string{}
	for _, d := range deliveries {
		key := d.Kind + ":" + d.Name
		declared[key] = true
		// take-delivery.sh is asked for a name, not a kind, so one name must
		// mean one delivery.
		if other, dup := names[d.Name]; dup {
			findings = append(findings, fmt.Sprintf("%s is declared as both %s and %s, and take-delivery.sh could not tell which to install", d.Name, other, key))
		}
		names[d.Name] = key

		s, ok := sections[key]
		if !ok {
			findings = append(findings, fmt.Sprintf("%s is declared as a %s delivery and has no section in the lock", d.Name, d.Kind))
			continue
		}
		if s.VersionKey != d.VersionKey || s.Version != d.Version {
			findings = append(findings, fmt.Sprintf("%s is pinned at %s=%s and the lock was made for %s=%s",
				d.Name, d.VersionKey, d.Version, s.VersionKey, s.Version))
		}
		for _, l := range s.Lines {
			if !lockHash.MatchString(l) {
				findings = append(findings, fmt.Sprintf("%s's section has a line with no SHA256, which installs whatever it finds: %s", d.Name, l))
			}
		}
		switch d.Kind {
		case "pypi":
			found := false
			for _, l := range s.Lines {
				if strings.HasPrefix(l, d.Name+"=="+d.Version+" ") {
					found = true
				}
			}
			if !found {
				findings = append(findings, fmt.Sprintf("%s's section does not pin %s itself at %s", d.Name, d.Name, d.Version))
			}
		case "fetch":
			want := strings.ReplaceAll(d.Spec, "{version}", d.Version)
			if u, err := url.Parse(want); err != nil || u.Scheme != "https" || u.Host == "" {
				findings = append(findings, fmt.Sprintf("%s fetches %q, which is not an https URL", d.Name, want))
			}
			if len(s.Lines) != 1 || !strings.HasPrefix(s.Lines[0], want+" --hash=sha256:") {
				findings = append(findings, fmt.Sprintf("%s's section must be exactly one line fetching %s, and is %q", d.Name, want, s.Lines))
			}
		}
	}
	findings = append(findings, disagreements(sections)...)
	var stale []string
	for key := range sections {
		if !declared[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	for _, key := range stale {
		findings = append(findings, fmt.Sprintf("the lock has a section for %s, which no tools: entry declares", key))
	}
	return findings, nil
}

// disagreements is every package two Python sections lock differently.
//
// take-delivery.sh installs every Python tool it is asked for into one
// environment, with one pip run, so the sections it is given must be one
// resolution: a package two tools share is locked by the same line in both.
// Two orders made apart and merged together each pass on their own and
// cannot be installed together, and the image that takes both never builds.
func disagreements(sections map[string]lockSection) []string {
	lines := map[string]map[string][]string{}
	for _, s := range sections {
		if s.Kind != "pypi" {
			continue
		}
		for _, l := range s.Lines {
			pkg, _, _ := strings.Cut(l, "==")
			if lines[pkg] == nil {
				lines[pkg] = map[string][]string{}
			}
			lines[pkg][l] = append(lines[pkg][l], s.Name)
		}
	}
	var findings []string
	for pkg, by := range lines {
		if len(by) < 2 {
			continue
		}
		var each []string
		for l, tools := range by {
			sort.Strings(tools)
			version, _, _ := strings.Cut(strings.TrimPrefix(l, pkg+"=="), " ")
			each = append(each, version+" for "+strings.Join(tools, ", "))
		}
		sort.Strings(each)
		findings = append(findings, fmt.Sprintf("%s is locked differently by tools installed together (%s), so they cannot be installed together: they were ordered apart", pkg, strings.Join(each, "; ")))
	}
	sort.Strings(findings)
	return findings
}

func explainDeliveriesLock(findings []string) string {
	var b strings.Builder
	b.WriteString("refusing: " + lockPath + " does not match what it must be ordered from.\n\n")
	for _, f := range findings {
		fmt.Fprintf(&b, "  - %s\n", f)
	}
	b.WriteString(`
Anything the lock covers is installed only from it, by hash, so a lock that
has drifted installs something nobody pinned - or refuses a version somebody
did.

Change a version in scripts/versions.env, or a pypi: or fetch: field on a
tools: entry in scripts/approved-suppliers.yml, then order again rather than
editing the lock:

    task order-deliveries
`)
	return b.String()
}
