package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A hypervisor's hostname is a vault value, and whatever a resource is keyed
// by is part of its address. Every tool that touches a resource prints its
// address - a plan, an apply, the contractor's progress lines, a CI log - with
// no attribute printed at all, so a hostname used as a key is a hostname
// published (#585).
//
// So in OpenTofu the hostname is read in exactly one place, a map from the
// config's node key (node0) to the hostname, and that map is used in exactly
// one way: as the value of `node_name`, where it is an attribute. Anything
// else - a for_each over hostnames, a placement that names one - is refused
// here, before an estate exists to leak it. The as-built record refuses the
// same thing in the estate itself (asbuilt.Result.KeyedByAValue), for a key
// this cannot see coming.
func TestAHostnameIsNeverAResourceKey(t *testing.T) {
	root := repoRoot(t)
	files := tracked(t, func(rel string) bool {
		return strings.HasSuffix(rel, ".tf") && !strings.Contains(rel, "/tests/")
	})
	if len(files) < 10 {
		t.Fatalf("only %d OpenTofu files were read, so the enumeration has stopped matching", len(files))
	}
	lookups := 0
	for _, rel := range files {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		problems, n := hostnameMisuse(rel, string(body))
		lookups += n
		for _, p := range problems {
			t.Error(p)
		}
	}
	if lookups == 0 {
		t.Fatal("no node_name reads local.hostnames, so the one permitted use has moved and this checked nothing")
	}
}

var (
	hostnameRead   = regexp.MustCompile(`\.hostname\b`)
	hostnameLookup = regexp.MustCompile(`\blocal\.hostnames\b`)
	hostnamesLocal = regexp.MustCompile(`^\s*hostnames\s*=\s*\{ for k, h in local\.site\.hypervisor\.nodes : k => h\.hostname \}\s*$`)
	nodeNameLookup = regexp.MustCompile(`^\s*node_name\s*=\s*local\.hostnames\[[a-z_.]+\]\s*$`)
)

// hostnameMisuse is every line of one OpenTofu file that reads a hypervisor's
// hostname other than the two permitted ways, and how many permitted lookups
// it holds.
func hostnameMisuse(rel, body string) (problems []string, lookups int) {
	for i, line := range strings.Split(body, "\n") {
		code, _, _ := strings.Cut(line, "#")
		switch {
		case hostnameRead.MatchString(code) && !hostnamesLocal.MatchString(code):
			problems = append(problems, fmt.Sprintf("%s:%d reads a hypervisor's hostname. It is a vault value: read it only into the `hostnames` map, by the config's node key, so nothing can be keyed by it.", rel, i+1))
		case hostnameLookup.MatchString(code) && !nodeNameLookup.MatchString(code):
			problems = append(problems, fmt.Sprintf("%s:%d uses local.hostnames somewhere other than `node_name = local.hostnames[<key>]`. Anywhere else it can become a resource key, and a key is printed in every address.", rel, i+1))
		case nodeNameLookup.MatchString(code):
			lookups++
		}
	}
	return problems, lookups
}

func TestHostnameMisuseAllowsOnlyTheMapAndNodeName(t *testing.T) {
	cases := []struct {
		line    string
		problem string
		lookups int
	}{
		{"  hostnames  = { for k, h in local.site.hypervisor.nodes : k => h.hostname }", "", 0},
		{"  node_name = local.hostnames[each.value.hypervisor]", "", 1},
		{"  node_name    = local.hostnames[each.value]", "", 1},
		{"  # for_each = toset([for h in local.hypervisors : h.hostname])", "", 0},
		{"  for_each = toset([for h in local.hypervisors : h.hostname])", "reads a hypervisor's hostname", 0},
		{"  datastores = { for h in local.hypervisors : h.hostname => h.datastores }", "reads a hypervisor's hostname", 0},
		{"  for_each = toset(values(local.hostnames))", "uses local.hostnames somewhere other", 0},
		{"  hostname   = v.name", "", 0},
	}
	for _, c := range cases {
		problems, n := hostnameMisuse("x.tf", c.line)
		got := strings.Join(problems, "|")
		if (c.problem == "") != (got == "") || !strings.Contains(got, c.problem) || n != c.lookups {
			t.Errorf("%q: got %q and %d lookup(s), want %q and %d", c.line, got, n, c.problem, c.lookups)
		}
	}
}
