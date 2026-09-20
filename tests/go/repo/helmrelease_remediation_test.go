package repo

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A HelmRelease retries forever rather than stalling for a person.
//
// Flux gives up after a finite number of remediation attempts, sets Stalled,
// and then does nothing until somebody forces a reconcile by hand. That is a
// person at a keyboard for a blocker which has usually cleared on its own by
// the time they get there.
//
// It is not hypothetical. The monitoring stack could not be admitted until the
// namespace's Pod Security label arrived from OpenTofu; four attempts were
// spent long before it did; the release sat Stalled, and every Flux layer
// behind it stopped - including the workloads - until the operator was walked
// through an annotation at a terminal (#459).
//
// -1 retries indefinitely with backoff, so an ordering problem heals itself. A
// release that is genuinely broken then presents as a sustained Ready=False,
// which monitoring can see, rather than as a silence nobody is watching.
func TestEveryHelmReleaseRetriesRatherThanStalling(t *testing.T) {
	root := repoRoot(t)
	found := 0

	err := filepath.WalkDir(filepath.Join(root, "clusters"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !(strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml")) {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		dec := yaml.NewDecoder(strings.NewReader(string(body)))
		for {
			var doc helmReleaseDoc
			if dec.Decode(&doc) != nil {
				break
			}
			if doc.Kind != "HelmRelease" {
				continue
			}
			found++
			for _, phase := range []struct {
				name    string
				retries *int
			}{
				{"install", doc.Spec.Install.retries()},
				{"upgrade", doc.Spec.Upgrade.retries()},
			} {
				if phase.retries == nil {
					t.Errorf("%s: HelmRelease %s declares no spec.%s.remediation.retries.\n\n"+
						"It then takes Flux's default and stops retrying, so a blocker that clears "+
						"later needs somebody to force a reconcile by hand (#459). Declare -1.",
						rel, doc.Metadata.Name, phase.name)
					continue
				}
				if *phase.retries != -1 {
					t.Errorf("%s: HelmRelease %s retries %s %d times and then stalls.\n\n"+
						"A finite count means a release blocked by something that has not arrived yet - a "+
						"namespace label, a CRD, a volume - gives up and waits for a person, while every "+
						"Flux layer behind it waits too. -1 retries with backoff, and a release that is "+
						"really broken shows as a sustained Ready=False instead.",
						rel, doc.Metadata.Name, phase.name, *phase.retries)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking clusters/: %v", err)
	}
	if found == 0 {
		t.Error("no HelmRelease was found under clusters/, so this checked nothing - " +
			"either they have moved or this has stopped parsing them")
	}
}

// The shape of the two phases is identical, so one type serves both and an
// absent block reads as "not declared" rather than as zero - which is a real
// distinction here, because zero would mean "never retry".
type helmPhase struct {
	Remediation *struct {
		Retries *int `yaml:"retries"`
	} `yaml:"remediation"`
}

func (p *helmPhase) retries() *int {
	if p == nil || p.Remediation == nil {
		return nil
	}
	return p.Remediation.Retries
}

type helmReleaseDoc struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Install *helmPhase `yaml:"install"`
		Upgrade *helmPhase `yaml:"upgrade"`
	} `yaml:"spec"`
}
