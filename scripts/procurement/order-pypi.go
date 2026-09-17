// The PyPI kind of delivery: how a Python tool's declared version becomes
// pinned, hashed lines in scripts/deliveries.lock.
//
// ONE RESOLUTION, NOT ONE PER TOOL. The tools share a user install on a
// workstation, so two tools resolved apart could pin one dependency at two
// versions and overwrite each other. They are resolved together, once, and
// each tool's section is its own closure within that one consistent set - so a
// CI lane installs only what its tool needs, and installing several conflicts
// with nothing.
//
// FOR THE PYTHONS THAT RUN THEM. Resolution can depend on the interpreter, so
// it runs for each version in lockedPythons and refuses to order if they
// disagree. Which interpreter moves is a decision for a person, not something
// to paper over with environment markers.
//
// NO NEW SUPPLIER. pip resolves for a foreign interpreter without installing
// (`--dry-run --report`), and PyPI's JSON API publishes the SHA256 of every file
// it serves. Both were already in use.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// lockedPythons are the interpreters the PyPI sections must hold for.
//
// 3.12 is python3 on ubuntu-latest (Ubuntu 24.04), where CI installs the tools.
// 3.13 is python3 on Debian 13, the workstation scripts/install-dependencies.sh
// provisions. Moving either is a change to this list and a new order.
var lockedPythons = []string{"3.12", "3.13"}

// resolvedPackage is one package pip chose, with what it requires.
type resolvedPackage struct {
	Name     string
	Version  string
	Requires []string
}

// resolveFunc resolves the given requirements for one interpreter version.
type resolveFunc func(python string, requirements []string) (map[string]resolvedPackage, error)

// hashFunc returns the SHA256 of every file PyPI serves for one release.
type hashFunc func(name, version string) ([]string, error)

// pypiSections resolves every PyPI tool together and returns each one's section.
func pypiSections(tools []declaredDelivery, resolve resolveFunc, hashes hashFunc) ([]lockSection, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	var requirements []string
	for _, t := range tools {
		requirements = append(requirements, t.Spec+"=="+t.Version)
	}

	var set map[string]resolvedPackage
	for i, py := range lockedPythons {
		got, err := resolve(py, requirements)
		if err != nil {
			return nil, fmt.Errorf("resolving for Python %s: %w", py, err)
		}
		if i == 0 {
			set = got
			continue
		}
		if diff := differences(set, got); len(diff) > 0 {
			return nil, fmt.Errorf("Python %s and %s resolve differently:\n  %s\n\n"+
				"one lock cannot serve both. Decide which interpreter moves, change lockedPythons, and regenerate",
				lockedPythons[0], py, strings.Join(diff, "\n  "))
		}
	}

	closures := map[string][]string{}
	covered := map[string]bool{}
	for _, t := range tools {
		c, err := closureOf(t.Name, set)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.Name, err)
		}
		closures[t.Name] = c
		for _, n := range c {
			covered[n] = true
		}
	}
	var stray []string
	for n := range set {
		if !covered[n] {
			stray = append(stray, n)
		}
	}
	if len(stray) > 0 {
		sort.Strings(stray)
		return nil, fmt.Errorf("pip resolved %s, which no tool's dependency closure reaches.\n\n"+
			"The closure is computed from requires_dist, so this means it has missed a requirement - and a lock "+
			"section missing a package fails at install time rather than here. Fix the closure, not the lock",
			strings.Join(stray, ", "))
	}

	hashed := map[string][]string{}
	for n, p := range set {
		h, err := hashes(p.Name, p.Version)
		if err != nil {
			return nil, fmt.Errorf("hashing %s %s: %w", p.Name, p.Version, err)
		}
		if len(h) == 0 {
			return nil, fmt.Errorf("PyPI lists no files for %s %s, so it cannot be locked", p.Name, p.Version)
		}
		hashed[n] = h
	}

	var sections []lockSection
	for _, t := range tools {
		s := lockSection{Kind: "pypi", Name: t.Name, VersionKey: t.VersionKey, Version: t.Version}
		for _, n := range closures[t.Name] {
			p := set[n]
			line := normalizeName(p.Name) + "==" + p.Version
			for _, h := range hashed[n] {
				line += " --hash=sha256:" + h
			}
			s.Lines = append(s.Lines, line)
		}
		sections = append(sections, s)
	}
	return sections, nil
}

var nonAlnum = regexp.MustCompile(`[-_.]+`)

// normalizeName is PEP 503's name normalisation.
func normalizeName(name string) string {
	return strings.ToLower(nonAlnum.ReplaceAllString(name, "-"))
}

var requirementName = regexp.MustCompile(`^\s*([A-Za-z0-9][A-Za-z0-9._-]*)\s*(?:\[([^\]]*)\])?`)
var extraMarker = regexp.MustCompile(`extra\s*==\s*["']([^"']+)["']`)

// closureOf is every package in set that name needs, including itself.
//
// A requirement guarded by `extra == "x"` counts only when something asked for
// that extra. Any other marker is left to the resolution: a requirement pip did
// not resolve is not in set, and is skipped by being absent.
func closureOf(name string, set map[string]resolvedPackage) ([]string, error) {
	if _, ok := set[name]; !ok {
		return nil, fmt.Errorf("it was requested and pip did not resolve it")
	}
	extras := map[string]map[string]bool{}
	seen := map[string]bool{}
	queue := []string{name}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		seen[n] = true
		for _, req := range set[n].Requires {
			spec, marker, _ := strings.Cut(req, ";")
			if m := extraMarker.FindStringSubmatch(marker); m != nil && !extras[n][normalizeName(m[1])] {
				continue
			}
			rm := requirementName.FindStringSubmatch(spec)
			if rm == nil {
				continue
			}
			dep := normalizeName(rm[1])
			if _, ok := set[dep]; !ok {
				continue
			}
			grew := false
			for _, e := range strings.Split(rm[2], ",") {
				if e = normalizeName(strings.TrimSpace(e)); e != "" {
					if extras[dep] == nil {
						extras[dep] = map[string]bool{}
					}
					if !extras[dep][e] {
						extras[dep][e] = true
						grew = true
					}
				}
			}
			if !seen[dep] || grew {
				queue = append(queue, dep)
			}
		}
	}
	var out []string
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// differences lists how two resolutions disagree.
func differences(a, b map[string]resolvedPackage) []string {
	var out []string
	for n, p := range a {
		q, ok := b[n]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("%s %s only on the first", n, p.Version))
		case p.Version != q.Version:
			out = append(out, fmt.Sprintf("%s %s against %s", n, p.Version, q.Version))
		}
	}
	for n, q := range b {
		if _, ok := a[n]; !ok {
			out = append(out, fmt.Sprintf("%s %s only on the second", n, q.Version))
		}
	}
	sort.Strings(out)
	return out
}

// pipResolve asks pip what it would install for one interpreter, installing nothing.
func pipResolve(python string, requirements []string) (map[string]resolvedPackage, error) {
	dir, err := os.MkdirTemp("", "order-pypi-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	report := filepath.Join(dir, "report.json")
	args := []string{"-m", "pip", "install", "--dry-run", "--ignore-installed", "--quiet",
		"--report", report, "--target", filepath.Join(dir, "target"),
		"--python-version", python, "--implementation", "cp", "--abi", "cp" + strings.ReplaceAll(python, ".", ""),
		"--platform", "manylinux_2_28_x86_64", "--platform", "manylinux_2_17_x86_64",
		"--platform", "manylinux2014_x86_64", "--platform", "linux_x86_64",
		"--only-binary=:all:"}
	args = append(args, requirements...)
	cmd := exec.Command("python3", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("pip: %v\n%s", err, out)
	}
	body, err := os.ReadFile(report)
	if err != nil {
		return nil, err
	}
	return parsePipReport(body)
}

// parsePipReport reads pip's --report output.
func parsePipReport(body []byte) (map[string]resolvedPackage, error) {
	var r struct {
		Install []struct {
			Metadata struct {
				Name         string   `json:"name"`
				Version      string   `json:"version"`
				RequiresDist []string `json:"requires_dist"`
			} `json:"metadata"`
		} `json:"install"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("reading pip's report: %w", err)
	}
	if len(r.Install) == 0 {
		return nil, errors.New("pip's report installs nothing, so the resolution did not happen")
	}
	out := map[string]resolvedPackage{}
	for _, i := range r.Install {
		out[normalizeName(i.Metadata.Name)] = resolvedPackage{
			Name: i.Metadata.Name, Version: i.Metadata.Version, Requires: i.Metadata.RequiresDist,
		}
	}
	return out, nil
}

// pypiHashes reads the SHA256 of every file PyPI serves for one release.
func pypiHashes(name, version string) ([]string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get("https://pypi.org/pypi/" + name + "/" + version + "/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("PyPI answered %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parsePypiRelease(body)
}

// parsePypiRelease reads the file digests from PyPI's release JSON.
func parsePypiRelease(body []byte) ([]string, error) {
	var r struct {
		URLs []struct {
			Digests struct {
				SHA256 string `json:"sha256"`
			} `json:"digests"`
		} `json:"urls"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("reading PyPI's release: %w", err)
	}
	seen := map[string]bool{}
	var out []string
	for _, u := range r.URLs {
		if h := u.Digests.SHA256; h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	sort.Strings(out)
	return out, nil
}
