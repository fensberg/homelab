package phases

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"homelab/contractor/config"
	"homelab/contractor/internal/run"
	"homelab/contractor/steps"
	"homelab/details/tcp"
)

// NotFromHereError is a converge that cannot be done from where it is being
// run, having applied nothing.
//
// Building a template is the one step of a converge that needs SSH to the
// hypervisor, to bring the disk image into the machine. A converge started
// by a merge runs inside the cluster, where the hypervisor's SSH does not
// answer; one run from the workstation is on the hypervisor's own network,
// where it does. The first converge that had a template to build from a
// merge found that out by downloading the image, making a machine and then
// failing, which left a machine where a template should be and a state that
// had been written (#741).
//
// It is a failure of its own kind for what it does not say: the change is
// not at fault and returning the repository to the commit before would help
// nothing. It is a converge for somebody at the workstation.
type NotFromHereError struct {
	Site string
	// Templates is what there is to build, by the step's own label.
	Templates []string
}

func (e *NotFromHereError) Error() string {
	return fmt.Sprintf(`this converge has a template to build (%s), and it cannot be built from here.

Building a template needs SSH to the hypervisor, which does not answer from
this machine. A converge started by a merge runs inside the cluster, where
it never does. Run it from the workstation, which is on the hypervisor's own
network:

    task converge SITE=%s

Nothing has been changed`, strings.Join(e.Templates, ", "), e.Site)
}

// templatesCanBeBuiltFromHere refuses a converge that has a template to
// build when the hypervisors' SSH does not answer from this machine.
//
// A template is to be built when the state holds none: a site's own, and an
// untrusted zone's when the site has a zone. One that is already there is
// cloned from over the hypervisor's API, which needs no SSH, so a converge
// with nothing to build is never asked this and runs from anywhere.
func templatesCanBeBuiltFromHere(ctx *run.Context, cfg *config.Config, zones bool) error {
	wanted := map[string]string{steps.Template: "the site's"}
	if zones {
		wanted[steps.ZoneTemplate] = "an untrusted zone's"
	}
	var toBuild []string
	for address, whose := range wanted {
		// Not there, or could not be asked: either way it is not known to
		// be there, and the question below is cheap.
		listed, err := run.CmdOutputQuiet(ctx.Dir, "tofu", "state", "list", address)
		if err != nil || strings.TrimSpace(listed) == "" {
			toBuild = append(toBuild, whose)
		}
	}
	if len(toBuild) == 0 {
		return nil
	}
	sort.Strings(toBuild)
	for _, node := range cfg.Sites[ctx.Site].Hypervisor.Nodes {
		if !awaitTCP(tcp.Addr(node.IP, 22), 5*time.Second, time.Second) {
			return &NotFromHereError{Site: ctx.Site, Templates: toBuild}
		}
	}
	return nil
}
