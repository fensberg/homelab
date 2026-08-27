// Package config loads the homelab management config and derives the
// per-site network values that OpenTofu, Ansible and this program all need
// to agree on.
package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// The bounds and the vendor map below are this program's half of a contract
// it shares with management/cluster/registry.tf, which implements the same
// rules in HCL so that `tofu plan` fails on a bad config even when the start
// button is bypassed. Two implementations of one rule can drift, so they are
// named here rather than written inline, and
// config/contract_test.go asserts these values still match the ones in the
// OpenTofu source. Change one and the test names the other.
const (
	// OctetMin and OctetMax bound a site's declared octet. Kubernetes
	// defaults occupy 10.96.0.0/12 (services) and 10.244.0.0/16 (pods);
	// those are cluster-internal and never routed over the overlay, but
	// overlapping them makes debugging confusing.
	OctetMin = 1
	OctetMax = 95
)

// RequiredProvidersByConcern is the one vendor this code implements per
// concern. source_control is absent on purpose: Flux's GitRepository source
// speaks plain git over HTTPS and works against GitHub, GitLab or Gitea
// alike, so asserting a vendor the code does not depend on would be noise
// rather than a guard.
var RequiredProvidersByConcern = map[string]string{
	"hypervisor":      "proxmox",
	"overlay_network": "tailscale",
	"object_storage":  "cloudflare",
}

type Config struct {
	Organization  Organization    `json:"organization"`
	SourceControl SourceControl   `json:"source_control"`
	Sites         map[string]Site `json:"sites"`
}

type Organization struct {
	Name string `json:"name"`
}

type SourceControl struct {
	RepoURL string `json:"repo_url"`
	Token   string `json:"token"`
}

type Site struct {
	Name              string         `json:"name"`
	Octet             int            `json:"octet"`
	ControlPlaneCount int            `json:"control_plane_count"`
	Hypervisor        Hypervisor     `json:"hypervisor"`
	OverlayNetwork    OverlayNetwork `json:"overlay_network"`
	ObjectStorage     ObjectStorage  `json:"object_storage"`
	State             State          `json:"state"`
}

type Hypervisor struct {
	Provider      string          `json:"provider"`
	VaultProvider string          `json:"vault_provider"`
	TokenID       string          `json:"token_id"`
	TokenSecret   string          `json:"token_secret"`
	Nodes         map[string]Node `json:"nodes"`
}

type Node struct {
	Hostname string `json:"hostname"`
	IP       string `json:"ip"`
}

type OverlayNetwork struct {
	Provider      string `json:"provider"`
	VaultProvider string `json:"vault_provider"`
	Domain        string `json:"domain"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
}

type ObjectStorage struct {
	Provider        string `json:"provider"`
	VaultProvider   string `json:"vault_provider"`
	AccountID       string `json:"account_id"`
	AdminToken      string `json:"admin_token"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	Bucket          string `json:"bucket"`
}

type State struct {
	DBPassword      string `json:"db_password"`
	BackupRecipient string `json:"backup_recipient"`
}

// LoadRendered reads and parses the rendered config. Callers should have run
// the Render phase first; a missing file is the caller's mistake to report,
// not this function's.
func LoadRendered(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("rendered config not found at %s. Run the Render phase first: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("rendered config at %s is not valid JSON: %w", path, err)
	}
	return &cfg, nil
}

// SiteNetwork is everything derived from a site's declared octet: its
// addressing, EVPN identifiers, and the VM/hostnames that follow from it.
type SiteNetwork struct {
	Name        string // slug, used for VM/hostnames
	Key         string // the key in sites{}, e.g. "site0"
	Octet       int
	Label       string
	SiteCIDR    string
	NodeCIDR    string
	Gateway     string
	ASN         int
	VRFVNI      int
	VNetVNI     int
	NodeIPs     []string
	VMNames     []string
	Hypervisors []Node
}

var slugInvalid = regexp.MustCompile(`[^A-Za-z0-9]+`)

// ResolveSiteNetwork re-derives and re-validates a site's network every time
// it is called, from whatever is currently on disk, rather than caching -
// phases run as separate steps and the config does not change mid-run.
//
// Validation here mirrors registry.tf on purpose: two sites colliding on an
// octet, or a vault item attesting the wrong vendor, should fail here in
// milliseconds rather than after a provider round trip.
func ResolveSiteNetwork(cfg *Config, name string) (*SiteNetwork, error) {
	if len(cfg.Sites) == 0 {
		return nil, fmt.Errorf("the config defines no sites")
	}
	site, ok := cfg.Sites[name]
	if !ok {
		known := make([]string, 0, len(cfg.Sites))
		for k := range cfg.Sites {
			known = append(known, k)
		}
		sort.Strings(known)
		return nil, fmt.Errorf("unknown site '%s'. The config defines: %s", name, strings.Join(known, ", "))
	}

	// Octets are declared, not derived, so uniqueness has to be checked
	// rather than assumed - across every site, not just the selected one.
	seen := map[int][]string{}
	for key, s := range cfg.Sites {
		seen[s.Octet] = append(seen[s.Octet], key)
	}
	var dupes []string
	for octet, keys := range seen {
		if len(keys) > 1 {
			dupes = append(dupes, fmt.Sprintf("%d (%s)", octet, strings.Join(keys, ", ")))
		}
	}
	if len(dupes) > 0 {
		sort.Strings(dupes)
		return nil, fmt.Errorf("duplicate octet(s) in sites: %s. Each site owns 10.<octet>.0.0/16; two sites sharing one collide on the overlay network", strings.Join(dupes, ", "))
	}
	if site.Octet < OctetMin || site.Octet > OctetMax {
		return nil, fmt.Errorf("octet %d out of range for site '%s'. Use %d-%d; Kubernetes defaults occupy 10.96.0.0/12 and 10.244.0.0/16", site.Octet, name, OctetMin, OctetMax)
	}

	// Declare the vendor three times and make them agree: this code
	// implements one vendor per concern, the config declares one in
	// `provider` where it is reviewable in git, and the 1Password item
	// attests one in `vault_provider`, travelling with the credentials
	// themselves. registry.tf checks all three; so does this, so that a
	// value reaching a provider through the Go path is held to the same
	// standard as one reaching it through tofu.
	declared := map[string]string{
		"hypervisor":      site.Hypervisor.Provider,
		"overlay_network": site.OverlayNetwork.Provider,
		"object_storage":  site.ObjectStorage.Provider,
	}
	attested := map[string]string{
		"hypervisor":      site.Hypervisor.VaultProvider,
		"overlay_network": site.OverlayNetwork.VaultProvider,
		"object_storage":  site.ObjectStorage.VaultProvider,
	}
	// Sorted so a config with several concerns wrong always reports the
	// same one first, rather than whichever the map happened to yield.
	for _, concern := range slices.Sorted(maps.Keys(RequiredProvidersByConcern)) {
		want := RequiredProvidersByConcern[concern]
		if declared[concern] != want {
			return nil, fmt.Errorf("provider mismatch in sites.%s.%s - the config declares '%s' but this code implements '%s'. Change the code before changing the declaration", name, concern, declared[concern], want)
		}
		if strings.TrimSpace(attested[concern]) == "" {
			return nil, fmt.Errorf("sites.%s.%s has no vault_provider. The 1Password item must attest which vendor its credentials belong to", name, concern)
		}
		if attested[concern] != want {
			return nil, fmt.Errorf("vendor mismatch in sites.%s.%s - the config declares '%s' but the vault item attests '%s'. Either the wrong item is referenced, or its credentials were replaced without updating its provider field", name, concern, declared[concern], attested[concern])
		}
	}

	// A declaration only catches someone who updates the declaration. AKIA
	// and ASIA prefixes are AWS long-term and temporary credentials; R2
	// issues 32 hex characters, so this is positive identification, not a
	// heuristic.
	if strings.HasPrefix(site.ObjectStorage.AccessKeyID, "AKIA") || strings.HasPrefix(site.ObjectStorage.AccessKeyID, "ASIA") {
		return nil, fmt.Errorf("sites.%s.object_storage.access_key_id is an AWS credential (AKIA/ASIA prefix) but this site declares %s", name, site.ObjectStorage.Provider)
	}

	nodeKeys := make([]string, 0, len(site.Hypervisor.Nodes))
	for k := range site.Hypervisor.Nodes {
		nodeKeys = append(nodeKeys, k)
	}
	sort.Strings(nodeKeys)
	nodes := make([]Node, 0, len(nodeKeys))
	for _, k := range nodeKeys {
		nodes = append(nodes, site.Hypervisor.Nodes[k])
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("site '%s' has no hypervisor nodes", name)
	}
	if site.ControlPlaneCount < 1 {
		return nil, fmt.Errorf("site '%s' has control_plane_count %d; it must be at least 1", name, site.ControlPlaneCount)
	}

	o := site.Octet

	// Must match the site_name expression in variables.tf: lowercase, every
	// run of non-alphanumerics collapsed to a hyphen, trimmed. These become
	// Proxmox VM names, so "Sheridan Road Office" has to become
	// "sheridan-road-office".
	slug := strings.ToLower(strings.Trim(slugInvalid.ReplaceAllString(site.Name, "-"), "-"))
	if slug == "" {
		slug = name
	}
	label := site.Name
	if strings.TrimSpace(label) == "" {
		label = slug
	}

	nodeIPs := make([]string, site.ControlPlaneCount)
	vmNames := make([]string, site.ControlPlaneCount)
	for i := 0; i < site.ControlPlaneCount; i++ {
		nodeIPs[i] = fmt.Sprintf("10.%d.10.%d", o, 100+i)
		vmNames[i] = fmt.Sprintf("%s-cp-%02d", slug, i+1)
	}

	return &SiteNetwork{
		Name:        slug,
		Key:         name,
		Octet:       o,
		Label:       label,
		SiteCIDR:    fmt.Sprintf("10.%d.0.0/16", o),
		NodeCIDR:    fmt.Sprintf("10.%d.10.0/24", o),
		Gateway:     fmt.Sprintf("10.%d.10.1", o),
		ASN:         65000 + o,
		VRFVNI:      10000 + o,
		VNetVNI:     11000 + o,
		NodeIPs:     nodeIPs,
		VMNames:     vmNames,
		Hypervisors: nodes,
	}, nil
}

var opRefPattern = regexp.MustCompile(`op://[^\s}]+`)

// AssertRenderedConfigComplete proves that op inject actually substituted
// every op:// reference in the template. A blank 1Password field resolves
// to an empty string, which op inject reports as success; the empty value
// then travels all the way into a provider, where it surfaces as something
// like "credentials are empty" with no indication of which field is at
// fault. Comparing the template against the rendered output names the exact
// reference instead.
func AssertRenderedConfigComplete(templatePath, renderedPath string) error {
	tplLeaves, err := flattenFile(templatePath)
	if err != nil {
		return err
	}
	renderedLeaves, err := flattenFile(renderedPath)
	if err != nil {
		return err
	}

	var unresolved, empty []string
	for path, tplVal := range tplLeaves {
		s, ok := tplVal.(string)
		if !ok || !strings.Contains(s, "op://") {
			continue
		}
		ref := opRefPattern.FindString(s)
		actual, present := renderedLeaves[path]
		actualStr, _ := actual.(string)

		switch {
		case present && strings.Contains(actualStr, "op://"):
			unresolved = append(unresolved, fmt.Sprintf("  %s  <-  %s", path, ref))
		case !present || strings.TrimSpace(actualStr) == "":
			empty = append(empty, fmt.Sprintf("  %s  <-  %s", path, ref))
		}
	}

	if len(unresolved) > 0 {
		return fmt.Errorf("op inject did not substitute %d reference(s):\n\n%s\n\nThe item or field does not exist, or the path is misspelled. Check with:\n    op read \"<the reference above>\"",
			len(unresolved), strings.Join(unresolved, "\n"))
	}
	if len(empty) > 0 {
		return fmt.Errorf("%d vault field(s) resolved to an empty value:\n\n%s\n\nThe field exists but has no content. Fill it in, or remove the entry from config/management.tpl.json if this deployment does not need it.",
			len(empty), strings.Join(empty, "\n"))
	}
	return nil
}

func flattenFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var tree any
	if err := json.Unmarshal(data, &tree); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	out := map[string]any{}
	flattenLeaves(tree, "", out)
	return out, nil
}

func flattenLeaves(node any, path string, out map[string]any) {
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			childPath := k
			if path != "" {
				childPath = path + "." + k
			}
			flattenLeaves(child, childPath, out)
		}
	case []any:
		for i, child := range v {
			flattenLeaves(child, fmt.Sprintf("%s[%d]", path, i), out)
		}
	default:
		out[path] = v
	}
}
