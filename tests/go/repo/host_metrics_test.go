package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
	playbook := stripYAMLComments(readRepoFile(t, "management/hypervisor/hypervisor-prep.yml"))
	scrape := hostScrape(t)
	tofu := ""
	for _, body := range tofuSources(t) {
		tofu += body
	}

	scraped := scrape != ""
	installed := regexp.MustCompile(`(?m)^\s*-\s*` + regexp.QuoteMeta(hostExporterPackage) + `\s*$`).MatchString(playbook)
	written := strings.Contains(tofu, `resource "kubernetes_secret" "host_metrics"`) &&
		strings.Contains(tofu, `resource "kubernetes_endpoint_slice_v1" "hypervisor"`)

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

	if !regexp.MustCompile(`--web\.listen-address=\{\{\s*ansible_host\s*\}\}:9100`).MatchString(playbook) {
		t.Error("the host's exporter is not told to listen on the host's own address.\n\n" +
			"That is the address the Service OpenTofu writes points Prometheus at. On the site's " +
			"gateway it is inside the zone's VRF, where a program on the host can neither answer " +
			"nor be reached. Set --web.listen-address={{ ansible_host }}:9100.")
	}
	if !regexp.MustCompile(`client_auth_type:\s*RequireAndVerifyClientCert`).MatchString(playbook) {
		t.Error("the host's exporter does not ask its caller for a certificate.\n\n" +
			"Anything that can reach the site's gateway - every pod in the cluster - would read " +
			"the host. Set client_auth_type: RequireAndVerifyClientCert.")
	}
	if !regexp.MustCompile(`(?m)^\s*scheme:\s*HTTPS\s*$`).MatchString(scrape) {
		t.Error("the host is scraped in the clear. The exporter serves TLS and nothing else; set scheme: HTTPS.")
	}
}

// The exporter has one name, and four files spell it: the certificate is
// generated for it, the playbook asks for it, the scrape verifies it, and the
// Service that makes it resolve is named for it. One that disagrees is a
// certificate that does not match, found on the estate.
func TestTheHostsExporterHasOneName(t *testing.T) {
	declared := regexp.MustCompile(`HostMetricsName = "([a-z0-9.-]+)"`).FindStringSubmatch(
		readRepoFile(t, "scripts/contractor/internal/phases/secrets.go"))
	if declared == nil {
		t.Fatal("the contractor no longer declares the name the exporter's certificate is generated for, so this check is asserting nothing")
	}
	name := declared[1]

	playbook := stripYAMLComments(readRepoFile(t, "management/hypervisor/hypervisor-prep.yml"))
	if got := regexp.MustCompile(`(?m)^\s*host_metrics_name:\s*"([^"]+)"`).FindStringSubmatch(playbook); got == nil || got[1] != name {
		t.Errorf("the playbook asks the exporter for %v, and its certificate is generated for %q.", got, name)
	}

	scrape := hostScrape(t)
	if !regexp.MustCompile(`(?m)^\s*serverName:\s*` + regexp.QuoteMeta(name) + `\s*$`).MatchString(scrape) {
		t.Errorf("the scrape does not verify the exporter as %q, the name in its certificate.", name)
	}
	if !regexp.MustCompile(`(?m)^\s*-\s*` + regexp.QuoteMeta(name) + `:9100\s*$`).MatchString(scrape) {
		t.Errorf("the scrape's target is not %s:9100, so it either cannot resolve or reaches something else.", name)
	}

	_, tofu := tofuDeclaring(t, `resource "kubernetes_service" "hypervisor"`)
	service := regexp.MustCompile(`(?s)resource "kubernetes_service" "hypervisor".*?name\s*=\s*"([a-z0-9-]+)"`).FindStringSubmatch(tofu)
	if service == nil || !strings.HasPrefix(name, service[1]+".monitoring.") {
		t.Errorf("the Service OpenTofu creates is %v, and %q does not resolve to it.", service, name)
	}
}

// hostScrape is the definition of the host's scrape in the Flux tree, without
// its comments, or "" when there is none.
func hostScrape(t *testing.T) string {
	t.Helper()
	path, err := fluxObjectPath("ScrapeConfig", "hypervisor")
	if err != nil {
		return ""
	}
	body, err := os.ReadFile(filepath.Join(repoRoot(t), path))
	if err != nil {
		t.Fatal(err)
	}
	return stripYAMLComments(string(body))
}
