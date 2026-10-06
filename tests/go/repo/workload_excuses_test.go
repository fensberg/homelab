package repo

import (
	"errors"
	"io"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// What a scanner is told not to say about a workload, and why it may be.
//
// Checkov is silenced in four ways about what this repository runs, and each
// silencing gives a reason about the workloads. A reason about a workload
// stops being true when the workload changes, and nothing reads the reason
// when it does. These read the workloads instead.

// A connection string written in the code carries no credential of its own.
//
// A secrets scanner reads `scheme://user:password@host` as a credential, and
// is told not to where the string is a format: every part filled in when it
// runs, from a value that came out of the vault. So wherever a connection
// string with a user part is written, the user part is placeholders and
// nothing else.
func TestAConnectionStringInTheCodeCarriesNoCredentialOfItsOwn(t *testing.T) {
	written := regexp.MustCompile(`[a-z][a-z0-9+.-]*://([^/"'\s@]+)@`)
	placeholder := regexp.MustCompile(`^(%[sdvq]|\$\{[^}]+\})(:(%[sdvq]|\$\{[^}]+\}))?$`)
	found := 0
	for _, rel := range trackedFiles(t) {
		if !strings.HasSuffix(rel, ".tf") && !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			continue
		}
		for i, line := range strings.Split(readRepoFile(t, rel), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, m := range written.FindAllStringSubmatch(line, -1) {
				found++
				if !placeholder.MatchString(m[1]) {
					t.Errorf("%s:%d writes a connection string whose user part is not a placeholder.\n\n"+
						"A secrets scanner is silenced on strings of this shape on the ground that they are "+
						"formats. One with a name or a password written into it is the thing the scanner is for.",
						rel, i+1)
				}
			}
		}
	}
	if found == 0 {
		t.Fatal("found no connection string with a user part, so nothing was checked and a silencing rests on nothing")
	}
}

// workload is one container of one workload this repository's own manifests
// declare.
type workloadContainer struct {
	file, workload, name string
	labels               map[string]string
	spec                 *yaml.Node
}

// ourWorkloads reads every Deployment, StatefulSet and DaemonSet in the
// repository's own manifests, and every Service. Files marked vendored are
// somebody else's and no scanner here reads them.
func ourWorkloads(t *testing.T) (containers []workloadContainer, selectors []map[string]string) {
	t.Helper()
	tracked := trackedFiles(t)
	vendored := vendoredFiles(t, tracked)
	for _, rel := range tracked {
		if vendored[rel] || !strings.HasSuffix(rel, ".yaml") && !strings.HasSuffix(rel, ".yml") || strings.HasPrefix(rel, ".github/") || testMaterial.MatchString(rel) {
			continue
		}
		dec := yaml.NewDecoder(strings.NewReader(readRepoFile(t, rel)))
		for {
			var doc yaml.Node
			err := dec.Decode(&doc)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("%s does not parse as YAML, so the workloads in it were not read: %v", rel, err)
			}
			if len(doc.Content) == 0 {
				continue
			}
			root := doc.Content[0]
			kind := mappingValue(root, "kind")
			if kind == nil {
				continue
			}
			spec := mappingValue(root, "spec")
			name := ""
			if md := mappingValue(root, "metadata"); md != nil {
				if n := mappingValue(md, "name"); n != nil {
					name = n.Value
				}
			}
			switch kind.Value {
			case "Service":
				selectors = append(selectors, stringMap(mappingValue(spec, "selector")))
			case "Deployment", "StatefulSet", "DaemonSet":
				template := mappingValue(spec, "template")
				labels := stringMap(mappingValue(mappingValue(template, "metadata"), "labels"))
				pod := mappingValue(template, "spec")
				if list := mappingValue(pod, "containers"); list != nil {
					for _, c := range list.Content {
						cn := ""
						if n := mappingValue(c, "name"); n != nil {
							cn = n.Value
						}
						containers = append(containers, workloadContainer{rel, kind.Value + "/" + name, cn, labels, c})
					}
				}
			}
		}
	}
	if len(containers) == 0 {
		t.Fatal("found no workload in the repository's own manifests, so nothing about one was checked")
	}
	return containers, selectors
}

func stringMap(n *yaml.Node) map[string]string {
	out := map[string]string{}
	if n == nil || n.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		out[n.Content[i].Value] = n.Content[i+1].Value
	}
	return out
}

// A container with no CPU limit reserves CPU, and is limited in memory.
//
// Checkov wants a CPU limit on every container and is told not to ask. A CPU
// limit is a quota: it stops a process at each boundary even on a node with
// cores free, which for a game people are moving around in is stutter. What
// protects the node is the request, which reserves room, and the memory
// limit, because memory cannot be throttled and the alternative to a limit
// is a neighbour evicted. So where there is no CPU limit, those two are
// there.
func TestAContainerWithNoCPULimitReservesCPUAndIsLimitedInMemory(t *testing.T) {
	containers, _ := ourWorkloads(t)
	var failures []string
	for _, c := range containers {
		resources := mappingValue(c.spec, "resources")
		limits, requests := stringMap(mappingValue(resources, "limits")), stringMap(mappingValue(resources, "requests"))
		if limits["cpu"] != "" {
			continue
		}
		if requests["cpu"] == "" {
			failures = append(failures, c.file+": "+c.workload+" container "+c.name+" has no CPU limit and reserves no CPU")
		}
		if limits["memory"] == "" {
			failures = append(failures, c.file+": "+c.workload+" container "+c.name+" has no CPU limit and no memory limit either")
		}
	}
	sort.Strings(failures)
	for _, f := range failures {
		t.Error(f + ".\n\nLeaving CPU unlimited is a decision the request and the memory limit make safe. Without them it is a container nothing bounds.")
	}
}

// No Secret is written in the tree.
//
// Checkov wants a secret mounted as a file and not handed over in an
// environment variable, and is told not to ask: the game server takes its
// password on its command line, so the value is in that container's process
// table whichever way it arrives. What matters about a secret a workload
// reads is where it came from, and that is held here. No manifest of this
// repository's own is a Secret: every one a workload names is written into
// the cluster by OpenTofu, from the vault.
func TestNoSecretIsWrittenInTheTree(t *testing.T) {
	tracked := trackedFiles(t)
	vendored := vendoredFiles(t, tracked)
	kind := regexp.MustCompile(`(?m)^kind:\s*Secret\s*$`)
	read := 0
	for _, rel := range tracked {
		if vendored[rel] || !strings.HasSuffix(rel, ".yaml") && !strings.HasSuffix(rel, ".yml") || testMaterial.MatchString(rel) {
			continue
		}
		read++
		if kind.MatchString(readRepoFile(t, rel)) {
			t.Errorf("%s is a Secret, written in the repository.\n\n"+
				"A secret a workload reads comes from the vault, through a Secret OpenTofu writes. "+
				"One in git is public, and a scanner is silenced about how secrets reach a workload "+
				"on the ground that none is.", rel)
		}
	}
	if read == 0 {
		t.Fatal("read no manifests, so nothing was checked")
	}
}

// Every image this repository builds is run by a manifest in it.
//
// Checkov wants a HEALTHCHECK in every Dockerfile and is told not to ask,
// because nothing built here runs anywhere but Kubernetes, and the kubelet
// does not read that instruction: what decides whether a container is healthy
// is the probes in its manifest. That is true of an image for as long as a
// manifest here runs it. One built and run by nothing here is run somewhere
// this repository cannot see, and may be somewhere a HEALTHCHECK is all there
// is.
func TestEveryImageBuiltHereIsRunByAManifestHere(t *testing.T) {
	tracked := trackedFiles(t)
	var manifests strings.Builder
	for _, rel := range tracked {
		if (strings.HasSuffix(rel, ".yaml") || strings.HasSuffix(rel, ".yml")) && !strings.HasPrefix(rel, ".github/") && !testMaterial.MatchString(rel) {
			manifests.WriteString(stripYAMLComments(readRepoFile(t, rel)))
		}
	}

	built := 0
	for _, rel := range tracked {
		if rel != "Dockerfile" && !strings.HasSuffix(rel, "/Dockerfile") {
			continue
		}
		built++
		// An image is named for what holds its Dockerfile: the application's
		// directory, or the directory of a work order, less its "-image".
		dir := strings.TrimSuffix(rel, "/Dockerfile")
		name := dir[strings.LastIndex(dir, "/")+1:]
		if name == "image" {
			parent := strings.TrimSuffix(dir, "/image")
			name = parent[strings.LastIndex(parent, "/")+1:]
		}
		name = strings.TrimSuffix(name, "-image")
		if !regexp.MustCompile(`image:\s*\S*-` + regexp.QuoteMeta(name) + `[@:]`).MatchString(manifests.String()) {
			t.Errorf("%s builds an image named for %q, and no manifest here runs one.\n\n"+
				"Checkov is told not to ask for a HEALTHCHECK because everything built here runs under "+
				"Kubernetes, whose probes are in the manifest. An image nothing here runs is not known to.",
				rel, name)
		}
	}
	if built == 0 {
		t.Fatal("found no Dockerfile, so nothing was checked")
	}
}
