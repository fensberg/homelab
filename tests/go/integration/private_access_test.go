//go:build integration

package integration_test

import (
	"strings"
	"testing"
	"time"

	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"homelab/details/capacity"
)

// A site's private access, asked of the cluster and not of the manifests.
//
// WHY THIS TIER. Everything a device reaches over the web passes four things
// that are each declared in git and each true only on a running site: the
// definitions a route is written in, the front door that reads them, the
// certificate it serves, and the Service an enrolled device is routed to.
// tests/go/repo holds the declarations to each other. It cannot see a
// certificate that was never issued, a route the front door refused, or a
// Service that selects no pod - and each of those leaves every pod Running
// and a name that does not load.
//
// The vendor is not asked here. Whether a name is answered for an enrolled
// device, and whether a device is carried to the address, are the estate's
// and need an enrolled device to ask.
const privateAccessNamespace = "traefik"

// condition is the status of one condition of an object, as kubectl prints
// it, or "" when the object has none of that type.
func condition(t *testing.T, namespace, kind, name, of string) string {
	t.Helper()
	out, err := k8s.RunKubectlAndGetOutputE(t, k8s.NewKubectlOptions("", kubeconfig(t), namespace),
		"get", kind, name, "-o", `jsonpath={.status.conditions[?(@.type=="`+of+`")].status}`)
	require.NoError(t, err, "asking the cluster for the %s %s", kind, name)
	return strings.TrimSpace(out)
}

// The definitions a Gateway and a route are written in are the cluster's,
// and the site's own Gateway is one the front door has taken up.
func TestTheSitesGatewayIsTakenUpByTheFrontDoor(t *testing.T) {
	assert.Equal(t, "True", condition(t, privateAccessNamespace, "gateways.gateway.networking.k8s.io", "private-access", "Accepted"),
		"the site's Gateway is not accepted, so nothing reads the listeners on it and no name is served")
	assert.Equal(t, "True", condition(t, privateAccessNamespace, "gateways.gateway.networking.k8s.io", "private-access", "Programmed"),
		"the site's Gateway is accepted and not programmed: the front door has it and is not serving it")
}

// Each certificate the site asked for was issued, is the short-lived kind,
// and has not been left to run down.
//
// A certificate that is never issued leaves its Secret absent and its
// listener refused, with nothing saying so outside cert-manager's own
// objects. One that stops being renewed is served until the day it is not.
//
// covers: integration:cert-manager
func TestEveryCertificateTheSiteAskedForIsIssuedAndBeingRenewed(t *testing.T) {
	opts := k8s.NewKubectlOptions("", kubeconfig(t), "")
	out, err := k8s.RunKubectlAndGetOutputE(t, opts, "get", "certificates.cert-manager.io", capacity.EveryNamespace, "-o",
		`jsonpath={range .items[*]}{.metadata.namespace}/{.metadata.name} {.status.conditions[?(@.type=="Ready")].status} {.status.notBefore} {.status.notAfter}{"\n"}{end}`)
	require.NoError(t, err, "asking the cluster for its certificates")
	asked := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		asked++
		if !assert.Len(t, fields, 4, "the certificate %s has no dates or no state: it was never issued", fields[0]) {
			continue
		}
		assert.Equal(t, "True", fields[1], "the certificate %s is not ready", fields[0])
		from, errFrom := time.Parse(time.RFC3339, fields[2])
		until, errUntil := time.Parse(time.RFC3339, fields[3])
		if !assert.NoError(t, errFrom) || !assert.NoError(t, errUntil) {
			continue
		}
		assert.LessOrEqual(t, until.Sub(from), 8*24*time.Hour,
			"the certificate %s is good for %s. The site asks for the kind that lasts about six days, so that one taken from it is worth days; a longer one means the issuer was not asked for that kind, or no longer gives it", fields[0], until.Sub(from))
		assert.Greater(t, time.Until(until), 24*time.Hour,
			"the certificate %s runs out within a day, at %s, and should have been renewed days ago", fields[0], until)
	}
	require.NotZero(t, asked, "the cluster has no certificate at all, so this checked nothing")
}

// The front door is behind the Service an enrolled device is routed to, and
// has taken up Grafana's route.
//
// covers: integration:traefik
func TestTheFrontDoorAnswersWhereAnEnrolledDeviceIsRouted(t *testing.T) {
	opts := k8s.NewKubectlOptions("", kubeconfig(t), privateAccessNamespace)
	behind, err := k8s.RunKubectlAndGetOutputE(t, opts, "get", "endpointslices",
		"-l", "kubernetes.io/service-name=private-access", "-o", "jsonpath={.items[*].endpoints[*].conditions.ready}")
	require.NoError(t, err, "asking what is behind the Service an enrolled device is routed to")
	assert.Contains(t, behind, "true",
		"nothing ready is behind the Service an enrolled device is routed to, so every name the site serves resolves, is carried to the site, and answers nobody")

	accepted, err := k8s.RunKubectlAndGetOutputE(t, k8s.NewKubectlOptions("", kubeconfig(t), monitoringNamespace),
		"get", "httproutes.gateway.networking.k8s.io", "grafana", "-o", `jsonpath={.status.parents[*].conditions[?(@.type=="Accepted")].status}`)
	require.NoError(t, err, "asking whether the front door took up Grafana's route")
	assert.Equal(t, "True", strings.TrimSpace(accepted),
		"Grafana's route is not accepted by the site's Gateway, so its name is served a certificate and then nothing")
}
