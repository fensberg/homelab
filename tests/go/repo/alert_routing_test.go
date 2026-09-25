package repo

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Where an alert lands is decided by the route tree in kube-prometheus-stack's
// values, and this walks that tree the way Alertmanager does - first matching
// child wins unless it says continue - for the alerts whose destination is a
// decision. Asserting the destination rather than the route's spelling: a
// route that still says InfoInhibitor but sits after a catch-all would pass a
// spelling check and page Slack every hour (#532).
func TestAlertsLandWhereTheEstateDecided(t *testing.T) {
	route := alertRoute(t)
	for _, c := range []struct {
		labels map[string]string
		want   string
		why    string
	}{
		{map[string]string{"alertname": "InfoInhibitor", "severity": "none"}, "nowhere",
			"the chart's helper alert, which exists only to inhibit others (#532)"},
		{map[string]string{"alertname": "Watchdog", "severity": "none"}, "slack-heartbeat",
			"the dead-man's switch, which must be delivered or it proves nothing"},
		{map[string]string{"alertname": "CPUThrottlingHigh", "severity": "info"}, "nowhere",
			"info-level alerts, recorded and not sent"},
		{map[string]string{"alertname": "etcdMembersDown", "severity": "critical"}, "slack",
			"a real alert"},
	} {
		if got := routeFor(route, c.labels); got != c.want {
			t.Errorf("%s lands on %q, want %q: %s", c.labels["alertname"], got, c.want, c.why)
		}
	}
}

type amRoute struct {
	Receiver string    `yaml:"receiver"`
	Matchers []string  `yaml:"matchers"`
	Continue bool      `yaml:"continue"`
	Routes   []amRoute `yaml:"routes"`
}

// routeFor returns the receiver an alert with these labels reaches. Only the
// equality matchers this estate writes are understood; any other operator
// fails the test rather than being read as a match or a miss.
func routeFor(r amRoute, labels map[string]string) string {
	for _, child := range r.Routes {
		if matches(child.Matchers, labels) {
			got := routeFor(child, labels)
			if got == "" {
				got = r.Receiver
			}
			if !child.Continue {
				return got
			}
		}
	}
	return r.Receiver
}

func matches(matchers []string, labels map[string]string) bool {
	for _, m := range matchers {
		name, value, ok := strings.Cut(m, "=")
		if !ok || strings.ContainsAny(name, "!~") {
			panic("unsupported matcher " + m + ": teach routeFor it rather than guessing")
		}
		if labels[strings.TrimSpace(name)] != strings.Trim(strings.TrimSpace(value), `"`) {
			return false
		}
	}
	return true
}

func alertRoute(t *testing.T) amRoute {
	t.Helper()
	const file = "clusters/management/infrastructure/controllers/kube-prometheus-stack.yaml"
	dec := yaml.NewDecoder(strings.NewReader(readRepoFile(t, file)))
	for {
		var doc struct {
			Kind string `yaml:"kind"`
			Spec struct {
				Values struct {
					Alertmanager struct {
						Config struct {
							Route amRoute `yaml:"route"`
						} `yaml:"config"`
					} `yaml:"alertmanager"`
				} `yaml:"values"`
			} `yaml:"spec"`
		}
		if dec.Decode(&doc) != nil {
			break
		}
		if doc.Kind == "HelmRelease" && doc.Spec.Values.Alertmanager.Config.Route.Receiver != "" {
			return doc.Spec.Values.Alertmanager.Config.Route
		}
	}
	t.Fatalf("%s holds no alertmanager route, so this checked nothing", file)
	return amRoute{}
}
