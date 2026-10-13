package repo

import (
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// Every machine a site builds is given its address when it is built.
//
// WHY. A machine boots with no configuration, and its configuration is sent
// to it over the network. So it needs an address before it has been told
// anything, and on these networks nothing hands one out: it is given with
// the machine, by the hypervisor. A machine built without one comes up
// with nothing, and the converge that built it waits for an API that
// nothing can reach. The first machine of an untrusted zone did exactly
// that, because the zone's was the one kind of machine written without it.
//
// A KIND, NOT A LIST. Every machine the hypervisor's provider is asked to
// make, wherever it is declared, and not the kinds there were when this was
// written. A template is not a machine: it is never started.
const machineResource = "proxmox_virtual_environment_vm"

// withoutAnAddress is every machine in the OpenTofu given that is built
// with no address of its own, by where it is declared.
func withoutAnAddress(sources map[string]string) (missing []string, machines int, err error) {
	for path, body := range sources {
		file, diags := hclsyntax.ParseConfig([]byte(body), path, hcl.InitialPos)
		if diags.HasErrors() {
			return nil, 0, diags
		}
		for _, block := range file.Body.(*hclsyntax.Body).Blocks {
			if block.Type != "resource" || len(block.Labels) != 2 || block.Labels[0] != machineResource {
				continue
			}
			if written(block.Body, "template") == cty.True {
				continue
			}
			machines++
			addressed := false
			for _, init := range block.Body.Blocks {
				if init.Type != "initialization" {
					continue
				}
				for _, ip := range init.Body.Blocks {
					for _, family := range ip.Body.Blocks {
						_, has := family.Body.Attributes["address"]
						addressed = addressed || (ip.Type == "ip_config" && has)
					}
				}
			}
			if !addressed {
				missing = append(missing, path+": "+block.Labels[0]+"."+block.Labels[1])
			}
		}
	}
	sort.Strings(missing)
	return missing, machines, nil
}

func TestEveryMachineIsGivenItsAddressWhenItIsBuilt(t *testing.T) {
	missing, machines, err := withoutAnAddress(tofuSources(t))
	if err != nil {
		t.Fatal(err)
	}
	if machines == 0 {
		t.Fatalf("no %s that is a machine was found, so this checked nothing", machineResource)
	}
	for _, m := range missing {
		t.Errorf("%s is built with no address of its own. It boots with nothing, on a network that hands nothing out, and its configuration cannot reach it: give it an initialization block with an ip_config, on the subnet it is on.", m)
	}
}

// The reading, on machines made up here.
func TestAMachineWithNoAddressIsSeenAndATemplateIsNotAMachine(t *testing.T) {
	const made = `
resource "proxmox_virtual_environment_vm" "addressed" {
  initialization {
    ip_config {
      ipv4 {
        address = "${each.value.ip}/24"
      }
    }
  }
}
resource "proxmox_virtual_environment_vm" "bare" {
  name = "x"
}
resource "proxmox_virtual_environment_vm" "told_only_its_resolvers" {
  initialization {
    dns { servers = ["192.0.2.1"] }
  }
}
resource "proxmox_virtual_environment_vm" "a_template" {
  template = true
}
resource "something_else" "ignored" {}
`
	missing, machines, err := withoutAnAddress(map[string]string{"made-up.tf": made})
	if err != nil || machines != 3 {
		t.Fatalf("read %d machine(s), %v", machines, err)
	}
	if got := strings.Join(missing, " "); got != "made-up.tf: proxmox_virtual_environment_vm.bare made-up.tf: proxmox_virtual_environment_vm.told_only_its_resolvers" {
		t.Errorf("the machines without an address were read as %q", got)
	}
	if _, _, err := withoutAnAddress(map[string]string{"broken.tf": `resource "x" {`}); err == nil {
		t.Error("OpenTofu that does not parse was read as building no machine")
	}
}
