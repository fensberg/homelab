package repo

import (
	"regexp"
	"testing"
)

// The host is measured, and it takes three halves in three tiers.
//
// A site's capacity is its host's, and until this the estate saw only the
// machines on it: how much memory the host really has, how hard it is pressed
// and how slow its disk is were nobody's to read
// (docs/epochs/04-observability.md, acceptance criterion 1).
//
// The exporter is installed by the hypervisor playbook. Where it answers is a
// node-network address, which this repository keeps out of git, so OpenTofu
// writes it into a secret. Prometheus scrapes what Flux substitutes from that
// secret. Any one of the three alone is wrong in its own way: a scrape with
// no exporter is a target that sits red, a scrape with no address is a
// HelmRelease that fails to substitute, and an exporter nothing reads is a
// port open on the hypervisor for no reason.
//
// And the exporter answers on the site's own gateway and nowhere else. It has
// no authentication. Bound to every address it would hand the host's facts to
// the LAN the hypervisor sits on.
const (
	hostExporterPackage = "prometheus-node-exporter"
	hostEndpointsVar    = "HYPERVISOR_ENDPOINTS"
)

func TestMeasuringTheHostChangesAllThreeHalvesTogether(t *testing.T) {
	_, stack := fluxObject(t, kindHelmRelease, "kube-prometheus-stack")
	playbook := stripYAMLComments(readRepoFile(t, "management/hypervisor/hypervisor-prep.yml"))
	varsPath, vars := tofuDeclaring(t, `resource "kubernetes_secret" "monitoring_vars"`)

	scraped := regexp.MustCompile(`targets:\s*\$\{` + hostEndpointsVar + `\}`).MatchString(stripYAMLComments(stack))
	installed := regexp.MustCompile(`(?m)^\s*-\s*` + regexp.QuoteMeta(hostExporterPackage) + `\s*$`).MatchString(playbook)
	written := regexp.MustCompile(hostEndpointsVar + `\s*=`).MatchString(vars)

	if scraped != installed || scraped != written {
		t.Errorf("measuring the host is half done.\n\n"+
			"  Prometheus scrapes ${%s}:            %v\n"+
			"  the playbook installs %s: %v\n"+
			"  %s writes %s: %v\n\n"+
			"All three, or none. A scrape without the exporter is a target that sits red; "+
			"without the address the HelmRelease fails to substitute; and an exporter nothing "+
			"reads is an open port on the hypervisor.",
			hostEndpointsVar, scraped, hostExporterPackage, installed, varsPath, hostEndpointsVar, written)
	}
	if !installed {
		return
	}
	if !regexp.MustCompile(`--web\.listen-address=\{\{\s*sdn_gateway\s*\}\}:9100`).MatchString(playbook) {
		t.Error("the host's exporter is not told to listen on the site's gateway alone.\n\n" +
			"It has no authentication, and its default is every address the hypervisor has, " +
			"which includes the LAN it sits on. Set --web.listen-address={{ sdn_gateway }}:9100.")
	}
}
