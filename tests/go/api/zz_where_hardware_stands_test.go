//go:build api

package api_test

// A one-off look, not part of the suite: what the cluster root recorded of
// the hypervisor against what the hypervisor says now, twice, a minute
// apart. Prints which readings differ and by how much. Datastores are shown
// by kind and not by name. Delete this file after running it.

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/gruntwork-io/terratest/modules/terraform"

	"homelab/contractor/config"
	"homelab/details/hypervisorapi"
	"homelab/tests/harness"
)

func flat(prefix string, v any, out map[string]string) {
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			flat(prefix+"."+k, c, out)
		}
	case []any:
		for i, c := range x {
			key := fmt.Sprint(i)
			if m, ok := c.(map[string]any); ok && m["id"] != nil {
				key = fmt.Sprintf("id=%v", m["id"])
			}
			flat(prefix+"["+key+"]", c, out)
		}
	default:
		out[prefix] = fmt.Sprint(x)
	}
}

// readings is one host's facts as path -> value, datastores renamed by kind.
func readings(t *testing.T, host any) map[string]string {
	raw, err := json.Marshal(host)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if stores, ok := m["storages"].(map[string]any); ok {
		names := make([]string, 0, len(stores))
		for n := range stores {
			names = append(names, n)
		}
		sort.Strings(names)
		renamed := map[string]any{}
		for i, n := range names {
			kind := "unknown"
			if s, ok := stores[n].(map[string]any); ok {
				kind = fmt.Sprint(s["type"])
			}
			renamed[fmt.Sprintf("%s#%d", kind, i)] = stores[n]
		}
		m["storages"] = renamed
	}
	out := map[string]string{}
	flat("", m, out)
	return out
}

func differences(t *testing.T, what string, a, b map[string]string) {
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	differ := 0
	for _, k := range sorted {
		if a[k] != b[k] {
			differ++
			was, is := a[k], b[k]
			if was == "" {
				was = "(absent)"
			}
			if is == "" {
				is = "(absent)"
			}
			t.Logf("DIFFERS  %s: %s%s  ->  %s", what, k, "  "+was, is)
		}
	}
	t.Logf("SUMMARY  %s: %d of %d readings differ", what, differ, len(sorted))
}

func TestWhereTheHardwareStands(t *testing.T) {
	site := harness.SiteConfig(t)
	node := harness.FirstHypervisorNode(t)
	auth := fmt.Sprintf("PVEAPIToken=%s=%s", site.Hypervisor.TokenID, site.Hypervisor.TokenSecret)
	ask := func() map[string]string {
		host, err := hypervisorapi.Survey(harness.HypervisorClient(t, 15*time.Second),
			fmt.Sprintf("https://%s:8006/api2/json", node.IP), node.Hostname, auth)
		if err != nil {
			t.Fatal(err)
		}
		return readings(t, host)
	}

	opts := harness.TofuOptions(t, config.ClusterRoot, nil)
	terraform.Init(t, opts)
	recordedJSON, err := terraform.OutputJsonE(t, opts, "hardware")
	if err != nil {
		t.Fatalf("could not read what the cluster root recorded: %v", err)
	}
	var recorded struct {
		Nodes map[string]any `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(recordedJSON), &recorded); err != nil {
		t.Fatal(err)
	}
	t.Logf("SUMMARY  the record holds %d hypervisor(s)", len(recorded.Nodes))

	now := ask()
	for key, host := range recorded.Nodes {
		differences(t, "recorded for "+key+" against now", readings(t, host), now)
	}
	time.Sleep(60 * time.Second)
	differences(t, "now against a minute later", now, ask())
}
