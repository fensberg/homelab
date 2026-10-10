// The engineer says what a site can carry.
//
// It takes what was measured of a site and what a change declares, and stamps
// the change or refuses it before it is built. It holds no key, reads no
// vault and changes nothing: everything it is given is a record that holds
// nothing real, and the repository itself. What a site is rated to carry is
// the operator's to decide, written where the engineer reads it; the
// engineer applies a rating and has no authority to set one.
package main

import (
	"archive/tar"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"homelab/details/applications"
	"homelab/details/capacity"
	"homelab/details/repopath"
)

const usage = `engineer says what a site can carry.

usage: engineer <verb> [flags]

verbs:
  stamp   Hold a change to what the site can hold. Counts what the change
          adds to what work that cannot wait reserves, against the site's
          capacity as last recorded, and refuses a change that leaves no
          room with a worker gone.
`

func main() {
	if len(os.Args) < 2 || os.Args[1] != "stamp" {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	here, err := repopath.Here()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	os.Exit(stamp(os.Args[2:], os.Stdout, os.Stderr, here, archived, kustomized))
}

// stamp is the verb, with the repository, the way a commit's tree is taken
// out and the way a directory is built handed to it.
func stamp(args []string, out, complaints io.Writer, repository string, treeAt func(repository, revision string) (string, func(), error), render renderer) int {
	fs := flag.NewFlagSet("stamp", flag.ContinueOnError)
	fs.SetOutput(complaints)
	site := fs.String("site", "", "The site the change is held to, by its key.")
	record := fs.String("record", "", "The directory the site's as-built record was saved to.")
	base := fs.String("base", "", "The commit the change is a change from.")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *site == "" || *record == "" || *base == "" {
		fmt.Fprintln(complaints, "error: stamp needs -site, -record and -base")
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(complaints, "error:", err)
		return 1
	}

	var standing capacity.Site
	read := ""
	raw, err := os.ReadFile(filepath.Join(*record, capacity.File))
	switch {
	case errors.Is(err, os.ErrNotExist):
		// Said below, and only refused if the change asks for something.
	case err != nil:
		return fail(err)
	default:
		var at string
		if standing, at, err = capacity.Unmarshal(raw); err != nil {
			return fail(err)
		}
		read = "as read " + at
	}

	// A workload that runs a pod on each worker asks for as many as there
	// are, which the site's own record says. With no record, one: the
	// change is refused below in any case if it asks for anything.
	workers := max(standing.Machines, 1)

	before, done, err := treeAt(repository, *base)
	if err != nil {
		return fail(fmt.Errorf("the tree this change is a change from could not be taken out, so nothing can be compared: %w", err))
	}
	defer done()

	was, err := reserved(before, *site, workers, render)
	if err != nil {
		return fail(fmt.Errorf("before the change: %w", err))
	}
	is, err := reserved(repository, *site, workers, render)
	if err != nil {
		return fail(fmt.Errorf("with the change: %w", err))
	}
	asked, grew := change(was, is)

	line := capacity.Of(standing, asked)
	if line.Unread() {
		fmt.Fprintf(out, "What %s's workers hold is not on record. This change adds %s and gives back %s.\n", *site, inGiB(asked.Added), inGiB(asked.Removed))
	} else {
		fmt.Fprintf(out, "%s, %s: %s\n", *site, read, line)
	}
	if len(grew) > 0 {
		fmt.Fprintf(out, "\nWhat asks for more than it did:\n  %s\n", strings.Join(grew, "\n  "))
	}
	if err := capacity.Refusal(line); err != nil {
		fmt.Fprintf(out, "\nREFUSED: %s\n", err)
		return 1
	}
	return 0
}

func inGiB(b int64) string { return fmt.Sprintf("%.1f GiB", float64(b)/float64(1<<30)) }

// archived takes a commit's tree out of the repository into a directory of
// its own: the parts a site's work is declared in, and nothing else.
func archived(repository, revision string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "engineer-before-")
	if err != nil {
		return "", nil, err
	}
	done := func() { _ = os.RemoveAll(dir) }
	// The revision is the operator's, or the workflow's own; it is passed
	// as one argument and never through a shell.
	// The whole tree is asked for and the parts wanted are kept. Asking git
	// for the parts fails outright when one of them is not there, and a
	// repository with no application yet is a repository, not an error.
	wanted := []string{applications.SitesDir + "/", applications.Dir + "/"}
	cmd := exec.Command("git", "-C", repository, "archive", "--format=tar", revision)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		done()
		return "", nil, fmt.Errorf("git archive %s: %w: %s", revision, err, strings.TrimSpace(stderr.String()))
	}
	tr := tar.NewReader(bytes.NewReader(raw))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			done()
			return "", nil, err
		}
		keep := false
		for _, w := range wanted {
			keep = keep || strings.HasPrefix(h.Name, w)
		}
		if !keep || h.Typeflag != tar.TypeReg {
			continue
		}
		target := filepath.Join(dir, filepath.FromSlash(h.Name))
		if !strings.HasPrefix(target, dir+string(os.PathSeparator)) {
			done()
			return "", nil, fmt.Errorf("the archive holds %q, which is outside it", h.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			done()
			return "", nil, err
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			done()
			return "", nil, err
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			done()
			return "", nil, err
		}
	}
	return dir, done, nil
}

// kustomized builds a directory the way Flux will, with the cluster's own
// tool. An application's settings patch its base, and only what comes out
// says what it reserves.
func kustomized(dir string) ([]byte, error) {
	cmd := exec.Command("kubectl", "kustomize", dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("kubectl kustomize: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
