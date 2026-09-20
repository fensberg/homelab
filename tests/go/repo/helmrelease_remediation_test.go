package repo

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A HelmRelease retries rather than stalling for a person.
//
// Flux's default strategy gives up after a finite number of attempts, sets
// Stalled, and then does nothing until somebody forces a reconcile by hand -
// a person at a keyboard for a blocker which has usually cleared on its own by
// the time they get there. The monitoring stack could not be admitted until
// the namespace's Pod Security label arrived from OpenTofu; its attempts were
// spent long before that; and every Flux layer behind it stopped until the
// operator was walked through an annotation at a terminal (#459).
//
// The fix is NOT unlimited remediation retries. Install remediation is an
// uninstall between every attempt, so `retries: -1` on a permanently broken
// release uninstalls and reinstalls it indefinitely, and the API documents no
// backoff on that path.
//
// RetryOnFailure retries on a fixed interval and applies no remediation. An
// ordering problem heals itself; a release that is really broken retries
// quietly and shows as a sustained Ready=False, which monitoring can see.
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
				name     string
				strategy string
			}{
				{"install", doc.Spec.Install.strategyName()},
				{"upgrade", doc.Spec.Upgrade.strategyName()},
			} {
				if phase.strategy != "RetryOnFailure" {
					t.Errorf("%s: HelmRelease %s declares spec.%s.strategy.name = %q, not RetryOnFailure.\n\n"+
						"Any other setting gives up after a finite number of attempts and waits for a "+
						"person to force a reconcile, while every Flux layer behind it waits too (#459). "+
						"Unlimited remediation retries are not the answer either: install remediation "+
						"uninstalls between attempts, so a permanently broken release would tear itself "+
						"down and rebuild forever.",
						rel, doc.Metadata.Name, phase.name, phase.strategy)
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

// The shape of the two phases is identical, so one type serves both. An
// absent block reads as an empty strategy name, which is what the message
// reports: Flux's default is the one that stalls.
type helmPhase struct {
	Strategy *struct {
		Name string `yaml:"name"`
	} `yaml:"strategy"`
}

func (p *helmPhase) strategyName() string {
	if p == nil || p.Strategy == nil {
		return ""
	}
	return p.Strategy.Name
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
