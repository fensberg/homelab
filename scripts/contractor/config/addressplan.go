package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"homelab/details/repopath"
)

// The address plan's answer for one site, as the module computes it
// (modules/infrastructure/address-plan). Only the fields Go reads.
type plannedSite struct {
	Slug          string             `json:"slug"`
	SiteCIDR      string             `json:"site_cidr"`
	NodeCIDR      string             `json:"node_cidr"`
	NodeGateway   string             `json:"node_gateway"`
	ASN           int                `json:"asn"`
	VRFVNI        int                `json:"vrf_vni"`
	VNetVNI       int                `json:"vnet_vni"`
	TemplateVMID  int                `json:"template_vm_id"`
	StateDatabase Endpoint           `json:"state_database"`
	ControlPlanes map[string]machine `json:"control_planes"`
	Workers       map[string]machine `json:"workers"`
	DMZZones      map[string]zone    `json:"dmz_zones"`
	DMZ           map[string]machine `json:"dmz"`
}

type machine struct {
	IP        string `json:"ip"`
	Name      string `json:"name"`
	VMID      int    `json:"vm_id"`
	Zone      string `json:"zone"`
	HostOctet int    `json:"host_octet"`
}

type zone struct {
	CIDR    string `json:"cidr"`
	Gateway string `json:"gateway"`
	VNet    string `json:"vnet"`
	VNI     int    `json:"vni"`
}

// AddressPlanDir is the address plan module, relative to the repository.
const AddressPlanDir = "modules/infrastructure/address-plan"

// planInput is what the module computes from, and all it is given: no
// credential reaches it.
type planInput struct {
	Name              string             `json:"name"`
	Octet             int                `json:"octet"`
	ControlPlaneCount int                `json:"control_plane_count"`
	WorkerCount       int                `json:"worker_count"`
	DMZZones          map[string]DMZZone `json:"dmz_zones"`
	Hypervisor        struct {
		Nodes map[string]Node `json:"nodes"`
	} `json:"hypervisor"`
}

// Endpoint is a host and a port.
type Endpoint struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// askAddressPlan asks the address plan module for every site's plan.
//
// Asked, not recomputed: the module is the one implementation of the scheme,
// and the cluster root calls the same module (docs/epochs/02-abstraction.md,
// "The addressing scheme is computed once"). `tofu console` answers offline,
// with no provider, no credential and no init, in well under a second.
//
// The module is handed only what it computes from - names, octets, counts,
// zones and the hypervisors' names and addresses - never a credential, through
// a file in a private directory removed as soon as it has answered.
func askAddressPlan(sites map[string]Site) (map[string]plannedSite, error) {
	root, err := repopath.Root()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, filepath.FromSlash(AddressPlanDir))

	input := map[string]planInput{}
	for k, s := range sites {
		in := planInput{
			Name: s.Name, Octet: s.Octet,
			ControlPlaneCount: s.ControlPlaneCount, WorkerCount: s.WorkerCount,
			DMZZones: s.DMZZones,
		}
		in.Hypervisor.Nodes = s.Hypervisor.Nodes
		// Empty, never null: the module reads absent collections as empty,
		// and a JSON null is not absent.
		if in.DMZZones == nil {
			in.DMZZones = map[string]DMZZone{}
		}
		if in.Hypervisor.Nodes == nil {
			in.Hypervisor.Nodes = map[string]Node{}
		}
		input[k] = in
	}
	// A private directory per question: tofu console takes a lock on its state
	// path even with no state, so two callers sharing the module directory's
	// default path refuse each other. Each gets its own, and it all goes.
	scratch, err := os.MkdirTemp("", "address-plan-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	vars, err := os.Create(filepath.Join(scratch, "sites.tfvars.json"))
	if err != nil {
		return nil, err
	}
	if err := json.NewEncoder(vars).Encode(map[string]any{"sites": input}); err != nil {
		_ = vars.Close()
		return nil, err
	}
	if err := vars.Close(); err != nil {
		return nil, err
	}

	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	c := exec.Command("tofu", "console", "-no-color", "-state="+filepath.Join(scratch, "none.tfstate"), "-var-file="+vars.Name())
	c.Dir = dir
	c.Stdin = strings.NewReader("jsonencode(local.plan.sites)\n")
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("asking the address plan: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	// console prints the string it was asked for as an HCL string literal.
	var encoded string
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &encoded); err != nil {
		return nil, fmt.Errorf("the address plan's answer is not a string (%w): %s", err, strings.TrimSpace(out.String()))
	}
	var plan map[string]plannedSite
	if err := json.Unmarshal([]byte(encoded), &plan); err != nil {
		return nil, fmt.Errorf("the address plan's answer is not the plan: %w", err)
	}
	return plan, nil
}
