package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validSite() Site {
	return Site{
		Name:              "North Street Office",
		Octet:             10,
		ControlPlaneCount: 3,
		Hypervisor: Hypervisor{
			Provider:      "proxmox",
			VaultProvider: "proxmox",
			Nodes: map[string]Node{
				"node0": {Hostname: "hv0", IP: "10.10.0.5"},
			},
		},
		OverlayNetwork: OverlayNetwork{
			Provider:      "tailscale",
			VaultProvider: "tailscale",
		},
		ObjectStorage: ObjectStorage{
			Provider:        "cloudflare",
			VaultProvider:   "cloudflare",
			AccessKeyID:     "f1e2d3c4b5a697887766554433221100",
			SecretAccessKey: "shh",
			Bucket:          "state-bucket",
		},
	}
}

func TestResolveSiteNetwork_HappyPath(t *testing.T) {
	cfg := &Config{Sites: map[string]Site{"site0": validSite()}}

	net, err := ResolveSiteNetwork(cfg, "site0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if net.Name != "north-street-office" {
		t.Errorf("Name = %q, want slugified site name", net.Name)
	}
	if net.SiteCIDR != "10.10.0.0/16" {
		t.Errorf("SiteCIDR = %q", net.SiteCIDR)
	}
	if net.Gateway != "10.10.10.1" {
		t.Errorf("Gateway = %q", net.Gateway)
	}
	if net.ASN != 65010 {
		t.Errorf("ASN = %d, want 65010", net.ASN)
	}
	wantIPs := []string{"10.10.10.100", "10.10.10.101", "10.10.10.102"}
	if strings.Join(net.NodeIPs, ",") != strings.Join(wantIPs, ",") {
		t.Errorf("NodeIPs = %v, want %v", net.NodeIPs, wantIPs)
	}
	wantNames := []string{"north-street-office-cp-100", "north-street-office-cp-101", "north-street-office-cp-102"}
	if strings.Join(net.VMNames, ",") != strings.Join(wantNames, ",") {
		t.Errorf("VMNames = %v, want %v", net.VMNames, wantNames)
	}
}

func TestResolveSiteNetwork_SlugFallsBackToKeyWhenNameIsBlank(t *testing.T) {
	site := validSite()
	site.Name = ""
	cfg := &Config{Sites: map[string]Site{"site7": site}}

	net, err := ResolveSiteNetwork(cfg, "site7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if net.Name != "site7" {
		t.Errorf("Name = %q, want fallback to the map key", net.Name)
	}
}

func TestResolveSiteNetwork_UnknownSite(t *testing.T) {
	cfg := &Config{Sites: map[string]Site{"site0": validSite()}}

	_, err := ResolveSiteNetwork(cfg, "does-not-exist")
	if err == nil {
		t.Fatal("expected an error for an unknown site, got nil")
	}
	if !strings.Contains(err.Error(), "site0") {
		t.Errorf("error should list known sites, got: %v", err)
	}
}

func TestResolveSiteNetwork_NoSites(t *testing.T) {
	cfg := &Config{Sites: map[string]Site{}}

	_, err := ResolveSiteNetwork(cfg, "site0")
	if err == nil {
		t.Fatal("expected an error when the config defines no sites")
	}
}

func TestResolveSiteNetwork_DuplicateOctet(t *testing.T) {
	a, b := validSite(), validSite()
	cfg := &Config{Sites: map[string]Site{"site0": a, "site1": b}}

	_, err := ResolveSiteNetwork(cfg, "site0")
	if err == nil {
		t.Fatal("expected an error for two sites sharing an octet")
	}
	if !strings.Contains(err.Error(), "duplicate octet") {
		t.Errorf("error should name the duplicate octet, got: %v", err)
	}
}

func TestResolveSiteNetwork_OctetOutOfRange(t *testing.T) {
	for _, octet := range []int{0, -1, 96, 200} {
		site := validSite()
		site.Octet = octet
		cfg := &Config{Sites: map[string]Site{"site0": site}}

		_, err := ResolveSiteNetwork(cfg, "site0")
		if err == nil {
			t.Errorf("octet %d: expected an out-of-range error, got nil", octet)
		}
	}
}

func TestResolveSiteNetwork_VendorMismatch(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Site)
	}{
		{"hypervisor", func(s *Site) { s.Hypervisor.VaultProvider = "not-proxmox" }},
		{"overlay_network", func(s *Site) { s.OverlayNetwork.VaultProvider = "not-tailscale" }},
		{"object_storage", func(s *Site) { s.ObjectStorage.VaultProvider = "not-cloudflare" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			site := validSite()
			tt.mutate(&site)
			cfg := &Config{Sites: map[string]Site{"site0": site}}

			_, err := ResolveSiteNetwork(cfg, "site0")
			if err == nil {
				t.Fatalf("expected a vendor mismatch error for %s, got nil", tt.name)
			}
			if !strings.Contains(err.Error(), tt.name) {
				t.Errorf("error should name the concern %q, got: %v", tt.name, err)
			}
		})
	}
}

func TestResolveSiteNetwork_MissingVaultProvider(t *testing.T) {
	site := validSite()
	site.Hypervisor.VaultProvider = ""
	cfg := &Config{Sites: map[string]Site{"site0": site}}

	_, err := ResolveSiteNetwork(cfg, "site0")
	if err == nil {
		t.Fatal("expected an error for a missing vault_provider attestation")
	}
}

func TestResolveSiteNetwork_AWSShapedAccessKeyOnNonAWSProvider(t *testing.T) {
	for _, prefix := range []string{"AKIA", "ASIA"} {
		site := validSite()
		site.ObjectStorage.AccessKeyID = prefix + "IOSFODNN7EXAMPLE"
		cfg := &Config{Sites: map[string]Site{"site0": site}}

		_, err := ResolveSiteNetwork(cfg, "site0")
		if err == nil {
			t.Errorf("prefix %s: expected an AWS-credential-on-wrong-provider error, got nil", prefix)
		}
	}
}

func TestResolveSiteNetwork_NoHypervisorNodes(t *testing.T) {
	site := validSite()
	site.Hypervisor.Nodes = map[string]Node{}
	cfg := &Config{Sites: map[string]Site{"site0": site}}

	_, err := ResolveSiteNetwork(cfg, "site0")
	if err == nil {
		t.Fatal("expected an error when a site has no hypervisor nodes")
	}
}

func TestResolveSiteNetwork_ControlPlaneCountBelowOne(t *testing.T) {
	site := validSite()
	site.ControlPlaneCount = 0
	cfg := &Config{Sites: map[string]Site{"site0": site}}

	_, err := ResolveSiteNetwork(cfg, "site0")
	if err == nil {
		t.Fatal("expected an error for control_plane_count below 1")
	}
}

func writeJSON(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing fixture %s: %v", name, err)
	}
	return path
}

func TestAssertRenderedConfigComplete_AllResolved(t *testing.T) {
	dir := t.TempDir()
	tpl := writeJSON(t, dir, "tpl.json", `{"organization":{"name":"{{ op://homelab/organization/name }}"}}`)
	rendered := writeJSON(t, dir, "rendered.json", `{"organization":{"name":"Example Org"}}`)

	if err := AssertRenderedConfigComplete(tpl, rendered); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAssertRenderedConfigComplete_UnsubstitutedReference(t *testing.T) {
	dir := t.TempDir()
	tpl := writeJSON(t, dir, "tpl.json", `{"organization":{"name":"{{ op://homelab/organization/name }}"}}`)
	// op inject left the placeholder untouched - the item or field doesn't exist.
	rendered := writeJSON(t, dir, "rendered.json", `{"organization":{"name":"{{ op://homelab/organization/name }}"}}`)

	err := AssertRenderedConfigComplete(tpl, rendered)
	if err == nil {
		t.Fatal("expected an error for an unsubstituted op:// reference")
	}
	if !strings.Contains(err.Error(), "op://homelab/organization/name") {
		t.Errorf("error should name the exact reference, got: %v", err)
	}
}

func TestAssertRenderedConfigComplete_ResolvedToEmpty(t *testing.T) {
	dir := t.TempDir()
	tpl := writeJSON(t, dir, "tpl.json", `{"database":{"password":"{{ op://homelab/site0/database/password }}"}}`)
	// The field exists in the vault item but has no content.
	rendered := writeJSON(t, dir, "rendered.json", `{"database":{"password":""}}`)

	err := AssertRenderedConfigComplete(tpl, rendered)
	if err == nil {
		t.Fatal("expected an error for a field that resolved to empty")
	}
	if !strings.Contains(err.Error(), "empty value") {
		t.Errorf("error should describe the empty value, got: %v", err)
	}
}

func TestAssertRenderedConfigComplete_NonSecretPlaceholderFieldsIgnored(t *testing.T) {
	dir := t.TempDir()
	// octet and control_plane_count are declared plaintext, never op:// - the
	// assertion must only look at leaves that actually reference the vault.
	tpl := writeJSON(t, dir, "tpl.json", `{"sites":{"site0":{"octet":10,"control_plane_count":3}}}`)
	rendered := writeJSON(t, dir, "rendered.json", `{"sites":{"site0":{"octet":10,"control_plane_count":3}}}`)

	if err := AssertRenderedConfigComplete(tpl, rendered); err != nil {
		t.Fatalf("unexpected error for plaintext-by-design fields: %v", err)
	}
}

// One node, one number. The address, the name and the VM id all carry the host
// octet, so a machine seen in Proxmox, in kubectl and on the network is
// obviously the same machine without arithmetic.
//
// They used to disagree - .100 was cp-01 was vm 1000 - and each was defensible
// alone. This asserts they stay agreed, because the cost of them drifting
// apart is paid during an incident by whoever is cross-referencing three
// consoles.
func TestResolveSiteNetwork_AddressNameAndIDShareOneNumber(t *testing.T) {
	site := validSite()
	site.ControlPlaneCount = 3
	cfg := &Config{Sites: map[string]Site{"site0": site}}

	net, err := ResolveSiteNetwork(cfg, "site0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i, ip := range net.NodeIPs {
		lastOctet := ip[strings.LastIndex(ip, ".")+1:]
		if !strings.HasSuffix(net.VMNames[i], "-cp-"+lastOctet) {
			t.Errorf("node %d is %s but named %q; the name must end in the host octet", i, ip, net.VMNames[i])
		}
	}
}
