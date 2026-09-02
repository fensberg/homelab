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
// line check-inventory already draws between "the reference resolves" and "here is
// what it resolved to".
const planWithSecrets = `{
  "resource_changes": [
    {"address":"proxmox_virtual_environment_vm.talos_cp[3]","type":"proxmox_virtual_environment_vm",
     "change":{"actions":["create"],"before":null,
       "after":{"name":"example-cp-04","ipv4_addresses":["192.0.2.103"],"description":"secret-value-here"}}},
    {"address":"proxmox_virtual_environment_vm.talos_cp[4]","type":"proxmox_virtual_environment_vm",
     "change":{"actions":["create"],"before":null,
       "after":{"name":"example-cp-05"}}},
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
		"example-cp-04", "example-cp-05", // a site name
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

// The plan hung for ten minutes and produced nothing, because
// data.talos_cluster_health reads at plan time against every node the config
// declares - and the config had just been raised from three to five. It waited
// for two machines that did not exist yet.
//
// Counting what the state actually holds is what turns that into an immediate
// answer. Only control-plane instances count: the template VM is a
// proxmox_virtual_environment_vm too, and counting it would make a correct
// estate look like it was one node short.
func TestCountControlPlaneVMs(t *testing.T) {
	const stateList = `cloudflare_r2_bucket.homelab
data.talos_client_configuration.this
proxmox_download_file.talos_disk_image["martha"]
proxmox_virtual_environment_vm.talos_template["martha"]
proxmox_virtual_environment_vm.talos_cp[0]
proxmox_virtual_environment_vm.talos_cp[1]
proxmox_virtual_environment_vm.talos_cp[2]
talos_machine_bootstrap.this
tailscale_tailnet_key.hypervisor`

	if got := countControlPlaneVMs(stateList); got != 3 {
		t.Fatalf("counted %d control-plane VMs, want 3", got)
	}
}

func TestCountControlPlaneVMs_IgnoresTheTemplateAlone(t *testing.T) {
	const onlyTemplate = `proxmox_virtual_environment_vm.talos_template["martha"]`
	if got := countControlPlaneVMs(onlyTemplate); got != 0 {
		t.Fatalf("the template VM was counted as a control-plane node; got %d, want 0", got)
	}
}

func TestCountControlPlaneVMs_EmptyState(t *testing.T) {
	if got := countControlPlaneVMs(""); got != 0 {
		t.Fatalf("got %d for empty state, want 0", got)
	}
}

// A resource address is not the safe half of a plan.
//
// `for_each` over the config's hypervisor map keys the resource by the
// hypervisor's real name, so a plan prints a vault value in an address without
// printing any attribute at all. That reached a public Actions log and a pull
// request comment, because everything watching for leaks was watching values.
func TestForEachKeysThatCanCarryANameAreRedacted(t *testing.T) {
	got := redactKeys(`proxmox_download_file.talos_disk_image["some-hostname"]`)
	if strings.Contains(got, "some-hostname") {
		t.Errorf("a name-shaped for_each key survived redaction: %s", got)
	}
	if !strings.Contains(got, "proxmox_download_file.talos_disk_image") {
		t.Errorf("redaction destroyed the address as well as the key: %s", got)
	}
}

// Numeric keys stay. They come from octets and node numbering, carry no proper
// noun, and are the difference between "a control-plane VM is being replaced"
// and knowing which one - which is what a reviewer needs when a plan destroys.
func TestNumericKeysAreKept(t *testing.T) {
	got := redactKeys(`proxmox_virtual_environment_vm.talos_cp["100"]`)
	if got != `proxmox_virtual_environment_vm.talos_cp["100"]` {
		t.Errorf("a numeric key was redacted, losing which resource changed: %s", got)
	}
}

// An address with no key is untouched.
func TestUnkeyedAddressesAreUntouched(t *testing.T) {
	const addr = "tailscale_tailnet_key.hypervisor"
	if got := redactKeys(addr); got != addr {
		t.Errorf("redactKeys mangled an unkeyed address: %s", got)
	}
}

// The comment is deliberately plain: a heading naming the site, the change,
// and nothing else.
//
// The copy it replaced greeted the reader, signed itself, and restated what
// the output was - "Addresses and actions only, never a value" - which the
// artefact then contradicted by containing both a value and the whole run. A
// reader can see what the output is; the comment's job is to carry the change.
func TestTheCommentIsJustTheChange(t *testing.T) {
	body := commentBody("site0", "  replace  tailscale_tailnet_key.hypervisor")

	for _, unwanted := range []string{
		"contractor checking in", // a signature
		"never a value",          // a claim the body cannot keep on its own
		"This is how the estate", // a restatement of what the reader can see
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("the comment still carries %q", unwanted)
		}
	}

	if !strings.Contains(body, "## Plan — site0") {
		t.Error("the comment does not say which site it is about, which matters " +
			"the moment a pull request plans more than one")
	}
	if !strings.Contains(body, "tailscale_tailnet_key.hypervisor") {
		t.Error("the comment does not contain the change, which is its only job")
	}
}

// A later run must be able to find and replace this comment rather than adding
// another. The workflow posted a new one every time, so a pull request that
// planned three times carried three comments and the reader had to work out
// which was current.
func TestTheCommentCarriesAMarkerPerSite(t *testing.T) {
	a := commentBody("site0", "x")
	b := commentBody("site10", "x")

	if !strings.HasPrefix(a, commentMarker("site0")) {
		t.Error("the comment has no marker, so a later run cannot find it to update")
	}
	if commentMarker("site0") == commentMarker("site10") {
		t.Error("two sites share a marker, so planning both on one pull request " +
			"would have each overwrite the other")
	}
	if strings.Contains(b, commentMarker("site0")) {
		t.Error("a site's comment carries another site's marker")
	}
}
