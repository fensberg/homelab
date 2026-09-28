package repo

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Every address the tunnel routes is a Service's fixed address, and the reverse.
//
// management/tunnel-routes.json - read by the address plan, which turns each
// route's host number into every site's own address - gives an enrolled WARP
// device a route to a specific cluster address, and a Service somewhere in git
// sets `clusterIP` to that address so something answers there. The site root
// hands each address to Flux as ADDRESS_<NAME>, and the Service sets
// `clusterIP: ${ADDRESS_<NAME>}`. The two live in different files, read by
// different tools - OpenTofu and Flux - and neither would notice the other
// changing. A route to an address nothing answers on converges cleanly,
// enrolls cleanly, and reaches nothing; a Service at a fixed address nothing
// routes to is an address somebody believes the tunnel serves.
//
// Both sides are discovered: the routes from that file, every Service from
// every manifest in the repository. A Service with a hand-set address of any
// other kind is refused as well: the only reason to set one here is the
// tunnel, and a literal address is one site's, written where every site reads
// it (#536).
const tunnelRoutesFile = "management/tunnel-routes.json"

// routeVariable is the Flux variable a route's address arrives in, as the
// site root's cluster-vars names it.
func routeVariable(name string) string {
	return "${ADDRESS_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "}"
}

func TestEveryTunnelRouteIsAServiceAddressAndTheReverse(t *testing.T) {
	root := repoRoot(t)
	var file struct {
		Routes map[string]int `json:"routes"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, tunnelRoutesFile)), &file); err != nil {
		t.Fatalf("%s: %v", tunnelRoutesFile, err)
	}
	routes := map[string]string{} // variable -> name
	for name := range file.Routes {
		routes[routeVariable(name)] = name
	}
	if len(routes) == 0 {
		t.Fatalf("%s declares no route, so this checked nothing", tunnelRoutesFile)
	}

	services := map[string]string{} // clusterIP -> file
	var literal []string
	for _, dir := range []string{"clusters", "environments", "modules"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !(strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml")) {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			dec := yaml.NewDecoder(strings.NewReader(string(data)))
			for {
				var doc struct {
					Kind string `yaml:"kind"`
					Spec struct {
						ClusterIP string `yaml:"clusterIP"`
					} `yaml:"spec"`
				}
				if dec.Decode(&doc) != nil {
					break
				}
				if doc.Kind != "Service" {
					continue
				}
				switch ip := doc.Spec.ClusterIP; {
				case ip == "" || ip == "None":
				case strings.HasPrefix(ip, "${ADDRESS_"):
					services[ip] = rel
				default:
					literal = append(literal, fmt.Sprintf("%s sets clusterIP: %s", rel, ip))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}

	vars := make([]string, 0, len(routes))
	for v := range routes {
		vars = append(vars, v)
	}
	sort.Strings(vars)
	for _, v := range vars {
		if _, ok := services[v]; !ok {
			t.Errorf("the tunnel routes %s, and no Service in git sets clusterIP: %s.\n\n"+
				"An enrolled device would be given a route to an address nothing answers on. The "+
				"converge succeeds, enrolment succeeds, and the join times out with nothing naming why.",
				routes[v], v)
		}
	}
	for v, manifest := range services {
		if _, ok := routes[v]; !ok {
			t.Errorf("%s sets clusterIP: %s, and %s routes no such name.\n\n"+
				"A fixed address exists here only for the tunnel. Either add the route, or let "+
				"Kubernetes allocate the address.", manifest, v, tunnelRoutesFile)
		}
	}
	sort.Strings(literal)
	for _, l := range literal {
		t.Errorf("%s.\n\nA hand-set address is one site's address written where every site reads it. "+
			"Declare it in %s and set clusterIP from the variable the site hands Flux.", l, tunnelRoutesFile)
	}
}
