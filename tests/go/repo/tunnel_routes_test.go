package repo

import (
	"encoding/json"
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
// management/tunnel-routes.json - read by the site's tunnel.tf for the routes
// and by the estate's split tunnel for what a device sends - gives an enrolled
// WARP device a route to a
// specific cluster address, and a Service somewhere in git sets `clusterIP` to
// that address so something answers there. The two live in different files,
// read by different tools - OpenTofu and Flux - and neither would notice the
// other changing. A route to an address nothing answers on converges cleanly,
// enrolls cleanly, and reaches nothing; a Service at a fixed address nothing
// routes to is an address somebody believes the tunnel serves.
//
// Both sides are discovered: the routes from that file, every Service from
// every manifest in the repository. A Service with a hand-set address in the
// reserved band that the tunnel does not route is refused as well, because the
// only reason to set one here is the tunnel.
const tunnelRoutesFile = "management/tunnel-routes.json"

func TestEveryTunnelRouteIsAServiceAddressAndTheReverse(t *testing.T) {
	root := repoRoot(t)
	var file struct {
		Routes map[string]string `json:"routes"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, tunnelRoutesFile)), &file); err != nil {
		t.Fatalf("%s: %v", tunnelRoutesFile, err)
	}
	routes := map[string]string{} // address -> name
	for name, addr := range file.Routes {
		routes[addr] = name
	}
	if len(routes) == 0 {
		t.Fatalf("%s declares no route, so this checked nothing", tunnelRoutesFile)
	}

	services := map[string]string{} // clusterIP -> file
	for _, dir := range []string{"clusters", "environments", "modules"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !(strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml")) {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
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
				if doc.Kind == "Service" && strings.HasPrefix(doc.Spec.ClusterIP, "10.96.0.") {
					rel, _ := filepath.Rel(root, path)
					services[doc.Spec.ClusterIP] = rel
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}

	addrs := make([]string, 0, len(routes))
	for a := range routes {
		addrs = append(addrs, a)
	}
	sort.Strings(addrs)
	for _, a := range addrs {
		if _, ok := services[a]; !ok {
			t.Errorf("the tunnel routes %s (%s), and no Service in git sets clusterIP: %s.\n\n"+
				"An enrolled device would be given a route to an address nothing answers on. The "+
				"converge succeeds, enrolment succeeds, and the join times out with nothing naming why.",
				a, routes[a], a)
		}
	}
	for a, manifest := range services {
		if _, ok := routes[a]; !ok {
			t.Errorf("%s sets clusterIP: %s, and %s routes no such address.\n\n"+
				"A hand-set address in the reserved band exists here only for the tunnel. Either add it "+
				"to it, or let Kubernetes allocate the address.", manifest, a, tunnelRoutesFile)
		}
	}
}
