package repo

import (
	"strings"
	"testing"

	"homelab/contractor/steps"
)

// A machine is what the hypervisor's provider is asked to make. That every
// one is given its address when it is built is a configuration rule
// (policy/conftest/plan_machine_addresses.rego).
const machineResource = "proxmox_virtual_environment_vm"

// Every machine a site builds is built by a step of its own, in the phase
// that ends by waiting for every machine to answer.
//
// WHY. A converge applies in named steps, each to the addresses it names,
// and the phase that builds machines ends by waiting for all of them. A
// kind of machine no step names is left to the catch-all apply of the next
// phase - so the phase waits five minutes for a machine nothing has built
// yet, and stops. The first machine of an untrusted zone did that (#733).
// The whole-converge tests run a site with a zone now, which holds zones;
// this holds the next kind of machine nobody has thought of yet.
func TestEveryMachineIsBuiltByAStepBeforeItIsWaitedFor(t *testing.T) {
	built := map[string]bool{}
	for _, st := range steps.Of("compute") {
		for _, target := range st.Targets {
			built[target] = true
		}
	}
	if len(built) == 0 {
		t.Fatal("the phase that builds machines names no address at all, so this checked nothing")
	}
	machines := 0
	for _, file := range repository(t).OpenTofu(t) {
		path := file.Path
		for _, block := range file.Body.Blocks {
			if block.Type != "resource" || len(block.Labels) != 2 || block.Labels[0] != machineResource {
				continue
			}
			machines++
			named := false
			for target := range built {
				named = named || strings.HasSuffix(target, "."+block.Labels[0]+"."+block.Labels[1])
			}
			if !named {
				t.Errorf("%s: %s.%s is built by no step of the phase that builds machines. That phase ends by waiting for every machine the site has, so it would wait for this one before anything had made it. Name it in a step (scripts/contractor/steps).", path, block.Labels[0], block.Labels[1])
			}
		}
	}
	if machines == 0 {
		t.Fatalf("no %s was found, so this checked nothing", machineResource)
	}
}
