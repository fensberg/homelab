package repo

import (
	"go/ast"
	"go/token"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
	"gopkg.in/yaml.v3"

	"homelab/tests/parsed"
)

// The host is measured, and it takes three halves in three tiers.
//
// A site's capacity is its host's, and until this the estate saw only the
// machines on it: how much memory the host really has, how hard it is pressed
// and how slow its disk is were nobody's to read
// (docs/epochs/04-observability.md, acceptance criterion 1).
//
// The exporter is installed by the hypervisor playbook. Where it answers and
// what its scraper presents are written into the cluster by OpenTofu, because
// one is a node-network address and the other a credential. Prometheus
// scrapes by a definition in the Flux tree. Any one alone is wrong in its own
// way: a scrape with no exporter never finds a target, an exporter nothing
// reads is a port open on the hypervisor for no reason, and either without
// the address and the certificates cannot reach the other.
//
// Two properties of the exporter itself are held as well. It answers on the
// host's own address, which is where the Service points: its first address
// was the site's gateway, which is inside the zone's VRF and which nothing on
// the host could serve or reach. And it asks whoever connects for a
// certificate: that address is on the network the hypervisor sits on, and
// without it anything there reads the host.
const hostExporterPackage = "prometheus-node-exporter"

func TestMeasuringTheHostChangesAllThreeHalvesTogether(t *testing.T) {
	tree := repository(t)
	playbook := hostPlaybook(t)
	scrape, scraped := hostScrape(t)

	installed := slices.Contains(parsed.Values(playbook.doc), hostExporterPackage)
	// OpenTofu writes where the exporter answers, and the secret the scrape
	// reads its certificates from.
	written := len(tree.Blocks(t, "resource", "kubernetes_endpoint_slice_v1", "hypervisor")) == 1
	if scraped {
		written = written && writesSecret(t, tree, scrape.Spec.TLSConfig.CA.Secret.Name)
	}

	if scraped != installed || scraped != written {
		t.Fatalf("measuring the host is half done.\n\n"+
			"  the Flux tree defines the scrape:                     %v\n"+
			"  the playbook installs %s:        %v\n"+
			"  OpenTofu writes its address and the scraper's secret: %v\n\n"+
			"All three, or none. A scrape without the exporter never finds a target; an exporter "+
			"nothing reads is an open port on the hypervisor; and neither reaches the other "+
			"without the address and the certificates.",
			scraped, hostExporterPackage, installed, written)
	}
	if !installed {
		return
	}

	if !slices.Contains(playbook.exporterArgs, "--web.listen-address={{ansible_host}}:9100") {
		t.Error("the host's exporter is not told to listen on the host's own address.\n\n" +
			"That is the address the Service OpenTofu writes points Prometheus at. On the site's " +
			"gateway it is inside the zone's VRF, where a program on the host can neither answer " +
			"nor be reached. Set --web.listen-address={{ ansible_host }}:9100.")
	}
	if playbook.clientAuth != "RequireAndVerifyClientCert" {
		t.Error("the host's exporter does not ask its caller for a certificate.\n\n" +
			"Anything that can reach the site's gateway - every pod in the cluster - would read " +
			"the host. Set client_auth_type: RequireAndVerifyClientCert.")
	}
	if scrape.Spec.Scheme != "HTTPS" {
		t.Error("the host is scraped in the clear. The exporter serves TLS and nothing else; set scheme: HTTPS.")
	}
}

// The exporter has one name, and four files spell it: the certificate is
// generated for it, the playbook asks for it, the scrape verifies it, and the
// Service that makes it resolve is named for it. One that disagrees is a
// certificate that does not match, found on the estate.
func TestTheHostsExporterHasOneName(t *testing.T) {
	tree := repository(t)
	name := stringConstant(t, tree, "scripts/contractor/internal/phases/secrets.go", "HostMetricsName")
	if name == "" {
		t.Fatal("the contractor no longer declares the name the exporter's certificate is generated for, so this check is asserting nothing")
	}

	if got := hostPlaybook(t).name; got != name {
		t.Errorf("the playbook asks the exporter for %q, and its certificate is generated for %q.", got, name)
	}

	scrape, _ := hostScrape(t)
	if scrape.Spec.TLSConfig.ServerName != name {
		t.Errorf("the scrape does not verify the exporter as %q, the name in its certificate.", name)
	}
	var targets []string
	for _, c := range scrape.Spec.StaticConfigs {
		targets = append(targets, c.Targets...)
	}
	if !slices.Contains(targets, name+":9100") {
		t.Errorf("the scrape's target is not %s:9100, so it either cannot resolve or reaches something else.", name)
	}

	service, _ := parsed.Literal(parsed.Attribute(tree.Declaring(t, "resource", "kubernetes_service", "hypervisor").Body, "metadata", "name").Expr)
	if service.IsNull() || service.Type() != cty.String || !strings.HasPrefix(name, service.AsString()+".monitoring.") {
		t.Errorf("the Service OpenTofu creates is named %#v, and %q does not resolve to it.", service, name)
	}
}

// playbookFacts is what the hypervisor's playbook says of the exporter: the
// whole playbook as YAML, the arguments its service is started with, what
// its TLS config asks of a caller, and the name it is asked to answer to.
type playbookFacts struct {
	doc          any
	exporterArgs []string
	clientAuth   string
	name         string
}

// hostPlaybook reads the playbook, and the two files it writes for the
// exporter, each by its own parser: the TLS config as YAML, and the
// service's environment as shell.
func hostPlaybook(t *testing.T) playbookFacts {
	t.Helper()
	var out playbookFacts
	repository(t).YAMLInto(t, "management/hypervisor/hypervisor-prep.yml", &out.doc)
	for _, m := range mapsIn(out.doc) {
		if name, ok := m["host_metrics_name"].(string); ok {
			out.name = name
		}
		copied, ok := m["ansible.builtin.copy"].(map[string]any)
		if !ok {
			continue
		}
		dest, _ := copied["dest"].(string)
		content, _ := copied["content"].(string)
		switch dest {
		case "/etc/prometheus-node-exporter/web.yml":
			var web struct {
				TLS struct {
					ClientAuth string `yaml:"client_auth_type"`
				} `yaml:"tls_server_config"`
			}
			if err := yaml.Unmarshal([]byte(content), &web); err != nil {
				t.Fatalf("the exporter's TLS config, as the playbook writes it, is not YAML: %v", err)
			}
			out.clientAuth = web.TLS.ClientAuth
		case "/etc/default/prometheus-node-exporter":
			commands, err := parsed.WorkflowCommands(dest, content)
			if err != nil {
				t.Fatalf("the exporter's environment, as the playbook writes it, is not shell: %v", err)
			}
			for _, c := range commands {
				// Ansible renders each {{ expression }} before the service's
				// manager splits the line, so one is one word whatever its
				// spacing.
				args := jinja.ReplaceAllString(c.Assigns["ARGS"], "{{$1}}")
				out.exporterArgs = append(out.exporterArgs, strings.Fields(args)...)
			}
		}
	}
	return out
}

// jinja is an expression Ansible renders, with its spacing.
var jinja = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)

// mapsIn is every mapping anywhere in a decoded YAML document.
func mapsIn(doc any) []map[string]any {
	var out []map[string]any
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			out = append(out, v)
			for _, each := range v {
				walk(each)
			}
		case []any:
			for _, each := range v {
				walk(each)
			}
		}
	}
	walk(doc)
	return out
}

// scrapeDefinition is what the host's scrape says about where and how it
// reaches the exporter.
type scrapeDefinition struct {
	Spec struct {
		Scheme        string `yaml:"scheme"`
		StaticConfigs []struct {
			Targets []string `yaml:"targets"`
		} `yaml:"staticConfigs"`
		TLSConfig struct {
			ServerName string `yaml:"serverName"`
			CA         struct {
				Secret struct {
					Name string `yaml:"name"`
				} `yaml:"secret"`
			} `yaml:"ca"`
		} `yaml:"tlsConfig"`
	} `yaml:"spec"`
}

// hostScrape is the definition of the host's scrape in the Flux tree, and
// whether there is one.
func hostScrape(t *testing.T) (scrapeDefinition, bool) {
	t.Helper()
	var out scrapeDefinition
	if _, err := fluxObjectPath(t, "ScrapeConfig", "hypervisor"); err != nil {
		return out, false
	}
	_, docs := fluxObject(t, "ScrapeConfig", "hypervisor")
	for _, d := range docs {
		m, _ := d.(map[string]any)
		if m["kind"] != "ScrapeConfig" {
			continue
		}
		encoded, err := yaml.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if err := yaml.Unmarshal(encoded, &out); err != nil {
			t.Fatalf("the host's scrape is not the shape this reads: %v", err)
		}
		return out, true
	}
	return out, false
}

// writesSecret says whether OpenTofu writes a Kubernetes Secret of this name.
func writesSecret(t *testing.T, tree *parsed.Tree, name string) bool {
	t.Helper()
	for _, b := range tree.Blocks(t, "resource", "kubernetes_secret") {
		if a := parsed.Attribute(b.Body, "metadata", "name"); a != nil && isLiteral(a.Expr, name) {
			return true
		}
	}
	return false
}

// stringConstant is a string constant a Go file declares, or "" when it
// declares none by that name.
func stringConstant(t *testing.T, tree *parsed.Tree, rel, name string) string {
	t.Helper()
	f := tree.Go(t, token.NewFileSet(), rel)
	return stringConstants(map[string]*ast.File{rel: f})[name]
}
