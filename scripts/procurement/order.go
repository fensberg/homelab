// order writes scripts/deliveries.lock: every delivery this estate takes that
// has no lockfile of its own ecosystem, pinned by version AND by hash.
//
// PROCUREMENT DECIDES; SECURITY CHECKS. Choosing what may be delivered and
// recording it is procurement's work. `security guard-deliveries` then refuses
// a lock that disagrees with the declarations it was made from, and never
// writes one - a guard that rewrote the file it judged would be judging its
// own work. `task order-deliveries` runs both, so a lock this writes is one
// security has accepted before anybody commits it.
//
// NOT UNDER THE EXPEDITE CREDENTIAL. Expediting is the one duty in this
// program that holds a bypass, and ordering is not it. tests/go/repo refuses
// any workflow running this verb, and any expedite workflow running a verb
// that is not expedite-*.
//
// WHY A LOCK. A delivery pinned only by version fixes the thing named and lets
// everything it pulls in float. `checkov==3.3.17` resolves to over a hundred
// packages, each fetched fresh on every install within whatever ranges its
// maintainers declared - so a compromised release of any of them arrived
// without anything in this repository changing (#416). A binary pinned only by
// version is the same hope with fewer moving parts: a release asset replaced
// behind the same tag installs without complaint.
//
// ONE VERSION, ONE LOCKFILE. A delivery's version is decided in
// scripts/versions.env and nowhere else. What kind of delivery it is is one
// field on its tools: entry in scripts/approved-suppliers.yml - `pypi: checkov`,
// or `fetch: <url>`. The lock is generated from both, nobody edits it, and
// scripts/take-delivery.sh is the only thing that installs from it.
//
// A KIND IS A RESOLVER. Each section of the lock names its kind -
// `# [pypi: checkov CHECKOV_VERSION=3.3.17]` - and a kind turns a declared
// version into pinned, hashed lines. PyPI is order-pypi.go; a file fetched from
// a URL is order-fetch.go. A third is a new resolver, not a new mechanism.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Fixed paths rather than flags, for the reason security gives: a program that
// can be pointed at a different list can be pointed at an empty one.
const (
	lockPath      = "scripts/deliveries.lock"
	suppliersPath = "scripts/approved-suppliers.yml"
	versionsPath  = "scripts/versions.env"
)

// declaredDelivery is one tools: entry that says how it is delivered.
type declaredDelivery struct {
	Kind       string // "pypi" or "fetch"
	Name       string // the section's name, and what take-delivery.sh is asked for
	Source     string // the tools: entry's source, owner/repository
	Spec       string // the kind field's value: a package name, or a URL template
	VersionKey string
	Version    string
}

// lockSection is one delivery's section of the lock.
type lockSection struct {
	Kind       string
	Name       string
	VersionKey string
	Version    string
	Lines      []string
}

// deliveryKinds are the fields a tools: entry may declare, each with how the
// section's name is derived from the entry.
var deliveryKinds = map[string]func(source, spec string) string{
	// The PyPI package, normalised the way pip names it.
	"pypi": func(_, spec string) string { return normalizeName(spec) },
	// The last element of the source, because a URL names a file and a file
	// name carries a version and a platform: hadolint, not
	// hadolint-linux-x86_64.
	"fetch": func(source, _ string) string { return source[strings.LastIndex(source, "/")+1:] },
}

func order(args []string) int {
	fs := flag.NewFlagSet("order", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root, err := repositoryRoot()
	if err != nil {
		return orderRefuses(err.Error())
	}
	body, err := renderDeliveriesLock(root, pipResolve, pypiHashes, fetchHash)
	if err != nil {
		return orderRefuses(err.Error())
	}
	if err := os.WriteFile(filepath.Join(root, lockPath), []byte(body), 0o644); err != nil {
		return orderRefuses("writing " + lockPath + ": " + err.Error())
	}
	fmt.Printf("wrote %s - security guard-deliveries decides whether it stands\n", lockPath)
	return 0
}

func orderRefuses(msg string) int {
	fmt.Fprintln(os.Stderr, "procurement order: "+msg)
	return 1
}

// repositoryRoot walks up from the working directory to the checkout, so the
// verb works under `go run -C scripts/procurement .` as well as from the root.
func repositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("could not determine the working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("ran from %s, which is not inside a git checkout", dir)
		}
		dir = parent
	}
}

// renderDeliveriesLock reads the declarations and returns the lock's contents.
func renderDeliveriesLock(root string, resolve resolveFunc, hashes hashFunc, fetch fetchFunc) (string, error) {
	deliveries, err := declaredDeliveries(root)
	if err != nil {
		return "", err
	}
	byKind := map[string][]declaredDelivery{}
	for _, d := range deliveries {
		byKind[d.Kind] = append(byKind[d.Kind], d)
	}
	pypi, err := pypiSections(byKind["pypi"], resolve, hashes)
	if err != nil {
		return "", err
	}
	fetched, err := fetchSections(byKind["fetch"], fetch)
	if err != nil {
		return "", err
	}
	return formatDeliveriesLock(append(pypi, fetched...)), nil
}

// declaredDeliveries reads every tools: entry that declares a kind, with its
// version from versions.env.
func declaredDeliveries(root string) ([]declaredDelivery, error) {
	suppliers, err := os.ReadFile(filepath.Join(root, suppliersPath))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", suppliersPath, err)
	}
	versions, err := os.ReadFile(filepath.Join(root, versionsPath))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", versionsPath, err)
	}
	deliveries, err := deliveriesIn(string(suppliers), parseVersions(string(versions)))
	if err != nil {
		return nil, err
	}
	if len(deliveries) == 0 {
		return nil, errors.New("no tools: entry in " + suppliersPath + " declares how it is delivered, " +
			"so there is nothing to order - and this estate installs tools that are, so the read has stopped matching")
	}
	return deliveries, nil
}

var topLevelKey = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*:`)

// deliveriesIn reads the tools: entries that declare a kind.
func deliveriesIn(suppliers string, versions map[string]string) ([]declaredDelivery, error) {
	var out []declaredDelivery
	var cur *declaredDelivery
	inTools := false
	flush := func() error {
		defer func() { cur = nil }()
		if cur == nil || cur.Kind == "" {
			return nil
		}
		if cur.VersionKey == "" {
			return fmt.Errorf("the tools: entry for %s declares %s: and no version:, so nothing says which release to order", cur.Source, cur.Kind)
		}
		v, ok := versions[cur.VersionKey]
		if !ok || v == "" {
			return fmt.Errorf("the tools: entry for %s reads its version from %s, which %s does not set", cur.Source, cur.VersionKey, versionsPath)
		}
		cur.Version = v
		cur.Name = deliveryKinds[cur.Kind](cur.Source, cur.Spec)
		out = append(out, *cur)
		return nil
	}
	for _, line := range strings.Split(suppliers, "\n") {
		if topLevelKey.MatchString(line) {
			if err := flush(); err != nil {
				return nil, err
			}
			inTools = strings.HasPrefix(line, "tools:")
			continue
		}
		if !inTools {
			continue
		}
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "- source:") {
			if err := flush(); err != nil {
				return nil, err
			}
			cur = &declaredDelivery{Source: strings.TrimSpace(strings.TrimPrefix(t, "- source:"))}
			continue
		}
		if cur == nil {
			continue
		}
		if strings.HasPrefix(t, "version:") {
			cur.VersionKey = strings.TrimSpace(strings.TrimPrefix(t, "version:"))
			continue
		}
		for kind := range deliveryKinds {
			if strings.HasPrefix(t, kind+":") {
				if cur.Kind != "" {
					return nil, fmt.Errorf("the tools: entry for %s declares both %s: and %s:, and a delivery arrives one way", cur.Source, cur.Kind, kind)
				}
				cur.Kind = kind
				cur.Spec = strings.TrimSpace(strings.TrimPrefix(t, kind+":"))
			}
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// parseVersions reads KEY=VALUE lines.
func parseVersions(body string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

// formatDeliveriesLock renders the lock.
//
// One requirement per line, hashes included, so a section is a valid input for
// its installer on its own, and two sections can be combined and de-duplicated
// with sort -u.
func formatDeliveriesLock(sections []lockSection) string {
	var b strings.Builder
	b.WriteString(`# Every delivery this estate takes that has no lockfile of its own ecosystem,
# pinned by version and by hash - and, for a Python tool, everything it pulls in.
#
# GENERATED - do not edit. Run ` + "`task order-deliveries`" + ` after changing a version in
# scripts/versions.env or a pypi: or fetch: field on a tools: entry in
# scripts/approved-suppliers.yml. ` + "`security guard-deliveries`" + ` refuses a lock that
# disagrees with either, on every commit and before CI installs anything.
#
# Install from it with scripts/take-delivery.sh, which refuses any file whose
# hash is not listed here.
`)
	sort.Slice(sections, func(i, j int) bool {
		if sections[i].Kind != sections[j].Kind {
			return sections[i].Kind < sections[j].Kind
		}
		return sections[i].Name < sections[j].Name
	})
	for _, s := range sections {
		fmt.Fprintf(&b, "\n# [%s: %s %s=%s]\n", s.Kind, s.Name, s.VersionKey, s.Version)
		for _, l := range s.Lines {
			b.WriteString(l + "\n")
		}
		b.WriteString("# [end]\n")
	}
	return b.String()
}
