package tests

import (
	"fmt"
	"strings"
	"testing"
)

// The probes look for the port the Service sends the query port to.
//
// A probe here asks the kernel whether a socket is bound, and the kernel
// lists ports in hex - so the deployment carries 0999 where the Service
// carries 2457, and nothing but this holds the two together. Changing the
// port in one place leaves a manifest that validates and a server that is
// restarted every two minutes for not listening where nobody told it to.
func TestEveryProbeLooksForThePortTheServiceQueries(t *testing.T) {
	var services []struct {
		Spec struct {
			Ports []struct {
				Name       string `json:"name"`
				TargetPort int    `json:"targetPort"`
			} `json:"ports"`
		} `json:"spec"`
	}
	readManifest(t, "production/service.yaml", &services)
	query := 0
	for _, s := range services {
		for _, p := range s.Spec.Ports {
			if p.Name == "query" {
				query = p.TargetPort
			}
		}
	}
	if query == 0 {
		t.Fatal("production/service.yaml names no port `query`, so there is nothing to hold the probes to")
	}
	bound := fmt.Sprintf(":%04X ", query)

	// What a probe runs, whichever field of exec carries it.
	type probe struct {
		Exec map[string][]string `json:"exec"`
	}
	var deployments []struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Name      string `json:"name"`
						Startup   *probe `json:"startupProbe"`
						Readiness *probe `json:"readinessProbe"`
						Liveness  *probe `json:"livenessProbe"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	readManifest(t, "base/deployment.yaml", &deployments)
	seen := 0
	for _, d := range deployments {
		for _, c := range d.Spec.Template.Spec.Containers {
			for kind, p := range map[string]*probe{"startup": c.Startup, "readiness": c.Readiness, "liveness": c.Liveness} {
				seen++
				if p == nil {
					t.Errorf("container %s has no %s probe, so nothing asks whether it is listening", c.Name, kind)
					continue
				}
				var words []string
				for _, w := range p.Exec {
					words = append(words, w...)
				}
				if asked := strings.Join(words, " "); !strings.Contains(asked, bound) {
					t.Errorf(`the %s probe of container %s does not look for the query port.

The Service sends its query port to %d, which the kernel lists as %q, and the
probe runs:

    %s

A probe looking anywhere else never finds the server listening.`, kind, c.Name, query, strings.TrimSpace(bound), asked)
				}
			}
		}
	}
	if seen == 0 {
		t.Fatal("base/deployment.yaml holds no container, so this is reading the wrong shape")
	}
}
