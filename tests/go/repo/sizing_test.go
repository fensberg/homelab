package repo

import (
	"errors"
	"io"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"homelab/details/applications"
	"homelab/details/holds"
	"homelab/details/sizing"
)

// Everything that reserves says where its size came from.
//
// A site is packed tight on what its workloads reserve, and a reservation is
// only as good as how it was arrived at: the publisher's figure, somebody's
// estimate, or what the thing was seen to use under load. That used to be a
// comment beside the number, where nothing reads it. It is data now
// (scripts/details/sizing), and this is what keeps it whole: every workload
// in the repository's own manifests, and every release that makes pods, is
// named by a `sized_from` entry of whoever owns it - the application whose
// directory it is in, or the core - container by container, and no entry
// names something that is no longer there.
//
// A size nobody has accounted for is the one that gets cut on the strength
// of a quiet week.
func TestEverythingThatReservesSaysWhereItsSizeCameFrom(t *testing.T) {
	root := repoRoot(t)
	apps, err := applications.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	core, err := sizing.ReadCore(root)
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]sizing.Declared{holds.Core: core}
	paths := map[string]string{holds.Core: sizing.CoreFile}
	for _, app := range apps {
		declared[app.Name], paths[app.Name] = app.SizedFrom, app.Path
	}

	reserving := whatReserves(t)
	if len(reserving) == 0 {
		t.Fatal("found nothing in the repository that reserves, so this looked for nothing")
	}
	found := map[string]map[string]bool{}
	for _, r := range reserving {
		if found[r.owner] == nil {
			found[r.owner] = map[string]bool{}
		}
		found[r.owner][r.object] = true
		said := declared[r.owner][r.object]
		if said == nil {
			t.Errorf(`%s reserves (%s) and nothing says where its size came from.

%s owns it, and %s has no sized_from entry for %q. Say, for each container in
it, whether its figures are the publisher's, an estimate, or measured under
load - and if nobody knows, that is an estimate, and saying so is the point.`, r.file, r.object, r.owner, paths[r.owner], r.object)
			continue
		}
		want := append([]string{}, r.containers...)
		var got []string
		for c := range said {
			got = append(got, c)
		}
		sort.Strings(want)
		sort.Strings(got)
		if strings.Join(want, ",") != strings.Join(got, ",") {
			t.Errorf("%s: %s has the containers [%s], and %s says where the sizes of [%s] came from. One for each, and none for a container that is not there",
				r.file, r.object, strings.Join(want, ", "), paths[r.owner], strings.Join(got, ", "))
		}
	}
	for owner, objects := range declared {
		for object := range objects {
			if !found[owner][object] {
				t.Errorf("%s says where the size of %s came from, and nothing of that owner's reserves by that kind and name", paths[owner], object)
			}
		}
	}
}

// reserver is one object that reserves, whose it is, and the containers it
// has - or sizing.EveryPod, for a release whose pods are its chart's.
type reserver struct {
	file, owner, object string
	containers          []string
}

// whatReserves is every object in the repository's own manifests that makes
// pods: a Deployment, StatefulSet or DaemonSet, container by container, the
// ones that only run at start among them; a release of a chart; and a
// database cluster.
func whatReserves(t *testing.T) []reserver {
	t.Helper()
	tracked := trackedFiles(t)
	vendored := vendoredFiles(t, tracked)
	var out []reserver
	for _, rel := range tracked {
		if vendored[rel] || !strings.HasSuffix(rel, ".yaml") && !strings.HasSuffix(rel, ".yml") || strings.HasPrefix(rel, ".github/") || testMaterial.MatchString(rel) {
			continue
		}
		owner := holds.Core
		if rest, ok := strings.CutPrefix(rel, applications.Dir+"/"); ok {
			owner, _, _ = strings.Cut(rest, "/")
		}
		dec := yaml.NewDecoder(strings.NewReader(readRepoFile(t, rel)))
		for {
			var doc yaml.Node
			err := dec.Decode(&doc)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("%s does not parse as YAML, so what it reserves was not read: %v", rel, err)
			}
			if len(doc.Content) == 0 {
				continue
			}
			top := doc.Content[0]
			kind, md := mappingValue(top, "kind"), mappingValue(top, "metadata")
			if kind == nil || md == nil || mappingValue(md, "name") == nil {
				continue
			}
			object := kind.Value + "/" + mappingValue(md, "name").Value
			spec := mappingValue(top, "spec")
			switch kind.Value {
			case "Deployment", "StatefulSet", "DaemonSet":
				pod := mappingValue(mappingValue(spec, "template"), "spec")
				var names []string
				for _, list := range []string{"initContainers", "containers"} {
					if l := mappingValue(pod, list); l != nil {
						for _, c := range l.Content {
							if n := mappingValue(c, "name"); n != nil {
								names = append(names, n.Value)
							}
						}
					}
				}
				out = append(out, reserver{rel, owner, object, names})
			case kindHelmRelease:
				out = append(out, reserver{rel, owner, object, []string{sizing.EveryPod}})
			case "Cluster":
				if mappingValue(spec, "instances") != nil {
					out = append(out, reserver{rel, owner, object, []string{sizing.EveryPod}})
				}
			}
		}
	}
	return out
}
