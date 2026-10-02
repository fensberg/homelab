package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"homelab/details/applications"
)

// The tunnel and the cluster agree on every address an enrolled device is
// routed to.
//
// An application declares each of its routes - a Service's name and a host
// number - and the address plan turns the host number into every site's own
// address, which gives an enrolled WARP device a route to a specific cluster
// address. A Service in that application's own directory, of that name, sets
// `clusterIP` to that address so something answers there: the site hands each
// address to Flux as ADDRESS_<NAME>, and the Service sets
// `clusterIP: ${ADDRESS_<NAME>}`. The two are read by different tools -
// OpenTofu and Flux - and neither would notice the other changing. A route to
// an address nothing answers on converges cleanly, enrolls cleanly, and
// reaches nothing; a Service at a fixed address nothing routes to is an
// address somebody believes the tunnel serves.
//
// Both sides are discovered: the routes from every application's declaration,
// every Service from every manifest in the repository. A Service with a
// hand-set address of any other kind is refused as well: the only reason to
// set one here is the tunnel, and a literal address is one site's, written
// where every site reads it (#536).

// routeVariable is the Flux variable a route's address arrives in, as the
// platform's cluster-vars names it.
func routeVariable(name string) string {
	return "${ADDRESS_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "}"
}

// fixedService is a Service that sets its own address, and where.
type fixedService struct{ file, name, clusterIP string }

func TestEveryTunnelRouteIsAServiceAddressAndTheReverse(t *testing.T) {
	root := repoRoot(t)
	apps, err := applications.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	var services []fixedService
	for _, rel := range tracked(t, func(rel string) bool {
		return (strings.HasPrefix(rel, fluxTree+"/") || strings.HasPrefix(rel, "modules/")) &&
			(strings.HasSuffix(rel, ".yaml") || strings.HasSuffix(rel, ".yml"))
	}) {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		services = append(services, fixedServices(rel, string(data))...)
	}
	// No floor on how many routes there are: an estate with no application
	// has none. What this refuses is proved against applications written
	// here, in TestRouteProblemsNamesEachWayARouteAndItsServiceDisagree.
	for _, problem := range routeProblems(apps, services) {
		t.Error(problem)
	}
}

// fixedServices is every Service in one manifest that sets its own address.
func fixedServices(rel, body string) []fixedService {
	var out []fixedService
	dec := yaml.NewDecoder(strings.NewReader(body))
	for {
		var doc struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Spec struct {
				ClusterIP string `yaml:"clusterIP"`
			} `yaml:"spec"`
		}
		// A document that is not YAML this reads is not a Service: Helm
		// values and vendored manifests are in the same trees.
		if dec.Decode(&doc) != nil {
			break
		}
		if doc.Kind == "Service" && doc.Spec.ClusterIP != "" && doc.Spec.ClusterIP != "None" {
			out = append(out, fixedService{rel, doc.Metadata.Name, doc.Spec.ClusterIP})
		}
	}
	return out
}

// routeProblems is every way the declared routes and the Services that set
// their own address disagree.
func routeProblems(apps []applications.Application, services []fixedService) []string {
	var problems []string
	answered := map[string]bool{} // "<application>/<route>"
	for _, s := range services {
		var owner *applications.Application
		for i := range apps {
			if strings.HasPrefix(s.file, apps[i].Root+"/") {
				owner = &apps[i]
			}
		}
		switch {
		case !strings.HasPrefix(s.clusterIP, "${"):
			problems = append(problems, fmt.Sprintf("%s sets clusterIP: %s.\n\nA hand-set address is one site's address written where every site reads it. "+
				"Declare the Service as a route in its application's %s and set clusterIP from the variable the site hands Flux.", s.file, s.clusterIP, applications.Declaration))
		case owner == nil:
			problems = append(problems, fmt.Sprintf("%s sets clusterIP: %s, and it is not in an application's directory.\n\n"+
				"A fixed address exists here only for the tunnel, and a route is something an application declares.", s.file, s.clusterIP))
		default:
			if _, declared := owner.Routes[s.name]; !declared || s.clusterIP != routeVariable(s.name) {
				problems = append(problems, fmt.Sprintf("%s sets the Service %s to clusterIP: %s, and %s declares no route of that name for it to be.\n\n"+
					"A fixed address exists here only for the tunnel. Either declare the route - the Service's name, and %s as its address - or let Kubernetes allocate the address.",
					s.file, s.name, s.clusterIP, owner.Path, routeVariable(s.name)))
				continue
			}
			answered[owner.Name+"/"+s.name] = true
		}
	}
	for _, a := range apps {
		for route := range a.Routes {
			if !answered[a.Name+"/"+route] {
				problems = append(problems, fmt.Sprintf("%s declares the route %s, and no Service of that name in %s sets clusterIP: %s.\n\n"+
					"An enrolled device would be given a route to an address nothing answers on. The "+
					"converge succeeds, enrolment succeeds, and the join times out with nothing naming why.",
					a.Path, route, a.Root, routeVariable(route)))
			}
		}
	}
	sort.Strings(problems)
	return problems
}

// The check is held to what it claims, against applications and Services
// written here: a route answered by its own Service is accepted, and each way
// the two can disagree is named.
func TestRouteProblemsNamesEachWayARouteAndItsServiceDisagree(t *testing.T) {
	app := func(name string, routes map[string]int) applications.Application {
		root := applications.Dir + "/" + name
		return applications.Application{Name: name, Root: root, Path: root + "/" + applications.Declaration, Routes: routes}
	}
	door := app("thing", map[string]int{"front-door": 7})
	in := func(a applications.Application, name, ip string) fixedService {
		return fixedService{a.Root + "/production/service.yaml", name, ip}
	}
	for name, c := range map[string]struct {
		apps     []applications.Application
		services []fixedService
		want     []string
	}{
		"a route its own Service answers":    {[]applications.Application{door}, []fixedService{in(door, "front-door", "${ADDRESS_FRONT_DOOR}")}, nil},
		"no application at all":              {nil, nil, nil},
		"a route nothing answers":            {[]applications.Application{door}, nil, []string{"declares the route front-door, and no Service"}},
		"a Service under another name":       {[]applications.Application{door}, []fixedService{in(door, "back-door", "${ADDRESS_FRONT_DOOR}")}, []string{"declares the route front-door, and no Service", "sets the Service back-door"}},
		"a Service given another's address":  {[]applications.Application{door}, []fixedService{in(door, "front-door", "${ADDRESS_SIDE_DOOR}")}, []string{"declares the route front-door, and no Service", "sets the Service front-door"}},
		"a fixed address with no route":      {[]applications.Application{app("thing", nil)}, []fixedService{in(door, "front-door", "${ADDRESS_FRONT_DOOR}")}, []string{"declares no route of that name"}},
		"a literal address":                  {[]applications.Application{door}, []fixedService{in(door, "front-door", "10.196.40.7")}, []string{"declares the route front-door, and no Service", "sets clusterIP: 10.196.40.7"}},
		"a route answered by another's file": {[]applications.Application{door, app("other", nil)}, []fixedService{in(app("other", nil), "front-door", "${ADDRESS_FRONT_DOOR}")}, []string{"declares the route front-door, and no Service", "declares no route of that name"}},
		"a fixed address in the core":        {[]applications.Application{door}, []fixedService{in(door, "front-door", "${ADDRESS_FRONT_DOOR}"), {fluxTree + "/core/thing.yaml", "x", "${ADDRESS_FRONT_DOOR}"}}, []string{"not in an application's directory"}},
	} {
		got := routeProblems(c.apps, c.services)
		if len(got) != len(c.want) {
			t.Errorf("%s: want %d problem(s), got %d: %v", name, len(c.want), len(got), got)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(strings.Join(got, "\n"), w) {
				t.Errorf("%s: no problem says %q: %v", name, w, got)
			}
		}
	}

	found := fixedServices("f.yaml", "kind: Service\nmetadata: {name: a}\nspec: {clusterIP: None}\n---\nkind: Service\nmetadata: {name: b}\nspec: {clusterIP: \"${ADDRESS_B}\"}\n---\nkind: Service\nmetadata: {name: c}\nspec: {}\n---\nkind: ConfigMap\nmetadata: {name: d}\nspec: {clusterIP: 1.2.3.4}\n")
	if len(found) != 1 || found[0] != (fixedService{"f.yaml", "b", "${ADDRESS_B}"}) {
		t.Errorf("the Services that set their own address were read as %+v", found)
	}
	if routeVariable("front-door") != "${ADDRESS_FRONT_DOOR}" {
		t.Errorf("a route's variable is %s", routeVariable("front-door"))
	}
}
