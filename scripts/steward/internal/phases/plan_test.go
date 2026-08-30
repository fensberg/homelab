package phases

import (
	"strings"
	"testing"
)

// A plan holds every attribute of every resource it touches, and this
// repository keeps hostnames, addresses and credentials out of git on purpose.
// The repository is also public, which makes an Actions job summary and a pull
// request comment world-readable. So the summary this produces reports
// structure only - addresses and actions, never a value - which is the same
// line check-vault already draws between "the reference resolves" and "here is
// what it resolved to".
const planWithSecrets = `{
  "resource_changes": [
    {"address":"proxmox_virtual_environment_vm.talos_cp[3]","type":"proxmox_virtual_environment_vm",
     "change":{"actions":["create"],"before":null,
       "after":{"name":"sheridan-cp-04","ipv4_addresses":["10.10.10.103"],"description":"secret-value-here"}}},
    {"address":"proxmox_virtual_environment_vm.talos_cp[4]","type":"proxmox_virtual_environment_vm",
     "change":{"actions":["create"],"before":null,
       "after":{"name":"sheridan-cp-05"}}},
    {"address":"talos_machine_configuration_apply.control_plane[0]","type":"talos_machine_configuration_apply",
     "change":{"actions":["update"],"before":{"x":"old-token"},"after":{"x":"new-token"}}},
    {"address":"tailscale_tailnet_key.this","type":"tailscale_tailnet_key",
     "change":{"actions":["delete","create"],"before":{"key":"tskey-abc"},"after":{"key":"tskey-def"}}},
    {"address":"kubernetes_secret.state_db_credentials","type":"kubernetes_secret",
     "change":{"actions":["no-op"],"before":{"p":"hunter2"},"after":{"p":"hunter2"}}}
  ]
}`

func TestSummarisePlan_ReportsStructure(t *testing.T) {
	got, err := summarisePlan([]byte(planWithSecrets))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"proxmox_virtual_environment_vm.talos_cp[3]",
		"proxmox_virtual_environment_vm.talos_cp[4]",
		"talos_machine_configuration_apply.control_plane[0]",
		"tailscale_tailnet_key.this",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary omits the address %q; a reviewer cannot tell what is changing", want)
		}
	}
	// no-op resources are noise in a review, not information.
	if strings.Contains(got, "kubernetes_secret.state_db_credentials") {
		t.Error("summary lists a no-op resource; the point is to show what changes")
	}
}

// The property this whole design exists for.
func TestSummarisePlan_NeverLeaksAValue(t *testing.T) {
	got, err := summarisePlan([]byte(planWithSecrets))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, secret := range []string{
		"sheridan-cp-04", "sheridan-cp-05", // the site's real name
		"10.10.10.103", // addressing
		"secret-value-here", "old-token", "new-token",
		"tskey-abc", "tskey-def", "hunter2",
	} {
		if strings.Contains(got, secret) {
			t.Errorf("summary contains the value %q. This output is posted to a public pull request; it must report structure only", secret)
		}
	}
}

func TestSummarisePlan_CountsByAction(t *testing.T) {
	got, err := summarisePlan([]byte(planWithSecrets))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2 create, 1 update, 1 replace (delete+create counts once, as a replace).
	for _, want := range []string{"2 to add", "1 to change", "1 to replace"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary is missing %q; got:\n%s", want, got)
		}
	}
}

// A destroy is the line a reviewer must not skim past.
func TestSummarisePlan_CallsOutDestroys(t *testing.T) {
	const destroying = `{"resource_changes":[
	  {"address":"proxmox_virtual_environment_vm.talos_cp[4]","change":{"actions":["delete"],"before":{"n":"x"},"after":null}}]}`
	got, err := summarisePlan([]byte(destroying))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "1 to destroy") {
		t.Fatalf("a destroy was not counted; got:\n%s", got)
	}
	if !strings.Contains(strings.ToUpper(got), "DESTROY") {
		t.Error("a plan that destroys does not say so prominently; that is the one line a reviewer must not skim past")
	}
}

// An empty plan is a real and common answer, and saying "no changes" is more
// useful than an empty table.
func TestSummarisePlan_NoChanges(t *testing.T) {
	got, err := summarisePlan([]byte(`{"resource_changes":[]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(strings.ToLower(got), "no changes") {
		t.Errorf("an empty plan should say so plainly; got:\n%s", got)
	}
}

func TestSummarisePlan_RejectsGarbage(t *testing.T) {
	if _, err := summarisePlan([]byte("not json")); err == nil {
		t.Error("expected an error for output that is not a plan")
	}
}
