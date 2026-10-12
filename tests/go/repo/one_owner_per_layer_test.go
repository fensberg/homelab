package repo

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Each layer a request passes has one owner, and a tool that could do a
// neighbour's job is held from doing it.
//
// Three of the tools between a device and an application can do more than
// the one thing each is here for: the network can route the web, the front
// door can get certificates, and the vendor's edge can hold one. Each of
// those is somebody else's job (docs/epochs/04-observability.md), and a
// second owner of a job is how a site comes to have two certificates for a
// name and nobody sure which is served. The first two are held here, by
// reading what each tool is told. The third is the estate's, and is held
// where the estate is tested: a tunnel that serves a public name is refused
// by management/estate's own suite.

// document is the one document of a manifest that declares an object.
func document(t *testing.T, kind, name string) (path string, doc map[string]any) {
	t.Helper()
	path, body := fluxObject(t, kind, name)
	dec := yaml.NewDecoder(strings.NewReader(body))
	for {
		var each map[string]any
		err := dec.Decode(&each)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("%s declares the %s %s and is not YAML that can be read: %v", path, kind, name, err)
		}
		meta, _ := each["metadata"].(map[string]any)
		if each["kind"] == kind && meta["name"] == name {
			return path, each
		}
	}
	t.Fatalf("%s declares the %s %s and it could not be read out of it", path, kind, name)
	return "", nil
}

// routesTheWeb is every switch in the network's settings that has it take
// web traffic and route it by hostname.
func routesTheWeb(settings map[string]any) []string {
	var on []string
	for _, key := range []string{"enable-gateway-api", "enable-ingress-controller"} {
		if fmt.Sprint(settings[key]) == "true" {
			on = append(on, key)
		}
	}
	return on
}

func TestTheNetworkDoesNotRouteTheWeb(t *testing.T) {
	path, doc := document(t, "ConfigMap", "cilium-config")
	settings, _ := doc["data"].(map[string]any)
	if len(settings) == 0 {
		t.Fatalf("%s declares the network's settings and none could be read, so this checked nothing", path)
	}
	for _, key := range routesTheWeb(settings) {
		t.Errorf("%s sets %s. The network connects pods and says which may speak to which; taking a hostname to an application is the front door's, "+
			"and two things routing the web is two places a name can be answered from. If the network is to take the job over, that is a decision for the epoch record and the front door goes.", path, key)
	}
}

// getsCertificates is every way a proxy's settings have it get a certificate
// for itself, by where in the settings each is.
func getsCertificates(values map[string]any) []string {
	var ways []string
	if resolvers, _ := values["certificatesResolvers"].(map[string]any); len(resolvers) > 0 {
		ways = append(ways, "certificatesResolvers names one")
	}
	if disk, _ := values["persistence"].(map[string]any); disk["enabled"] == true {
		ways = append(ways, "persistence gives it a disk, which it has only certificates of its own to keep on")
	}
	// A resolver can be named in an argument or an environment variable as
	// well as in the chart's own setting for one, and the credential for a
	// domain is always handed over as a variable.
	said := func(where string, v any) {
		raw, _ := yaml.Marshal(v)
		text := strings.ToLower(string(raw))
		for _, word := range []string{"certificatesresolvers", "certresolver", "acme", "dns_api_token", "dns-token"} {
			if strings.Contains(text, word) {
				ways = append(ways, where+" speaks of "+word)
			}
		}
	}
	for _, where := range []string{"additionalArguments", "env", "envFrom", "ports", "volumes", "additionalVolumeMounts"} {
		if v, ok := values[where]; ok {
			said(where, v)
		}
	}
	sort.Strings(ways)
	return ways
}

func TestTheFrontDoorGetsNoCertificates(t *testing.T) {
	path, doc := document(t, "HelmRelease", "traefik")
	spec, _ := doc["spec"].(map[string]any)
	values, _ := spec["values"].(map[string]any)
	if len(values) == 0 {
		t.Fatalf("%s declares the front door and its settings could not be read, so this checked nothing", path)
	}
	for _, way := range getsCertificates(values) {
		t.Errorf("%s: %s. The front door serves certificates and gets none: getting them is cert-manager's, which holds the credential for the domain and is the one place a certificate is renewed. "+
			"A proxy that gets its own keeps them on one disk in one pod, and is a second owner of every name it serves.", path, way)
	}
}

// Both checks are held to what they claim, against settings written here.
func TestEachToolHeldToItsLayerIsCaughtDoingANeighboursJob(t *testing.T) {
	if got := routesTheWeb(map[string]any{"enable-l7-proxy": "true", "enable-gateway-api": "false"}); len(got) != 0 {
		t.Errorf("a network that does not route the web was read as doing so: %v", got)
	}
	if got := routesTheWeb(map[string]any{"enable-gateway-api": "true", "enable-ingress-controller": true}); len(got) != 2 {
		t.Errorf("each of the two switches is caught, as a word or as a truth; got %v", got)
	}

	quiet := map[string]any{
		"certificatesResolvers": map[string]any{},
		"persistence":           map[string]any{"enabled": false},
		"ports":                 map[string]any{"websecure": map[string]any{"expose": map[string]any{"default": false}}},
		"additionalArguments":   []any{"--log.level=INFO"},
	}
	if got := getsCertificates(quiet); len(got) != 0 {
		t.Errorf("a proxy told no way of getting a certificate was read as having one: %v", got)
	}
	for name, values := range map[string]map[string]any{
		"a resolver":             {"certificatesResolvers": map[string]any{"mine": map[string]any{}}},
		"a disk":                 {"persistence": map[string]any{"enabled": true}},
		"an argument":            {"additionalArguments": []any{"--certificatesresolvers.mine.acme.dnschallenge=true"}},
		"a credential":           {"env": []any{map[string]any{"name": "CF_DNS_API_TOKEN"}}},
		"the estate's own":       {"envFrom": []any{map[string]any{"secretRef": map[string]any{"name": "dns-token"}}}},
		"a resolver on one port": {"ports": map[string]any{"websecure": map[string]any{"tls": map[string]any{"certResolver": "mine"}}}},
	} {
		if got := getsCertificates(values); len(got) == 0 {
			t.Errorf("%s: a proxy given a way of getting a certificate was not caught", name)
		}
	}
}
