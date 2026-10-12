package repo

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Every alert the estate declares says what it means and the first thing to
// try, and the channel is shown both.
//
// An alert that says only that something is wrong is read by somebody who
// then has to work out what to do about it, usually tired and usually from a
// phone. On 2026-10-11 an application was down for five hours between its
// first alert and anybody acting on it. So each alert carries three things of
// its own: what it is (summary), what it means (description), and the first
// thing to try (first_step), kept apart from the rest so that it can be
// shown on a line of its own and so that leaving it out is something a
// machine can see (docs/epochs/04-observability.md, criterion 26).
//
// What this cannot hold is that the step is a good one. That is a person's
// to judge when the alert is written, and when it next fires.
//
// The alerts the monitoring stack brings with it are not the estate's and
// are not held here. They carry a page of their project's, which the
// channel is shown in place of a first step.

// alertRule is one alert as a manifest declares it.
type alertRule struct {
	file, name  string
	annotations map[string]string
}

// alertsIn is every alert in one manifest's PrometheusRules.
func alertsIn(rel, body string) ([]alertRule, error) {
	var out []alertRule
	dec := yaml.NewDecoder(strings.NewReader(body))
	for {
		var doc struct {
			Kind string `yaml:"kind"`
			Spec struct {
				Groups []struct {
					Rules []struct {
						Alert       string            `yaml:"alert"`
						Annotations map[string]string `yaml:"annotations"`
					} `yaml:"rules"`
				} `yaml:"groups"`
			} `yaml:"spec"`
		}
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s is not YAML that can be read: %w", rel, err)
		}
		if doc.Kind != "PrometheusRule" {
			continue
		}
		for _, g := range doc.Spec.Groups {
			for _, r := range g.Rules {
				if r.Alert != "" {
					out = append(out, alertRule{rel, r.Alert, r.Annotations})
				}
			}
		}
	}
}

// unsaid is everything an alert leaves out of what it owes whoever reads it.
func unsaid(a alertRule) []string {
	var problems []string
	said := func(key string) string { return strings.TrimSpace(a.annotations[key]) }
	for key, what := range map[string]string{
		"summary":     "what it is",
		"description": "what it means",
		"first_step":  "the first thing to try",
	} {
		if said(key) == "" {
			problems = append(problems, fmt.Sprintf("%s: the alert %s does not say %s (%s).", a.file, a.name, what, key))
		}
	}
	// A first step that only says the alert again is one more way of
	// leaving it out.
	if step := said("first_step"); step != "" && (step == said("summary") || step == said("description")) {
		problems = append(problems, fmt.Sprintf("%s: the alert %s gives as its first step what it has already said. Say what to do.", a.file, a.name))
	}
	sort.Strings(problems)
	return problems
}

func TestEveryAlertSaysWhatToTryFirst(t *testing.T) {
	root := repoRoot(t)
	found := 0
	for _, rel := range tracked(t, func(rel string) bool {
		return (strings.HasPrefix(rel, fluxTree+"/") || strings.HasPrefix(rel, "modules/")) &&
			(strings.HasSuffix(rel, ".yaml") || strings.HasSuffix(rel, ".yml"))
	}) {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		// Only a manifest that speaks of the kind is read as one: charts'
		// values and vendored output are in the same trees and are not all
		// YAML this can decode.
		if !strings.Contains(string(data), "kind: PrometheusRule") {
			continue
		}
		alerts, err := alertsIn(rel, string(data))
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range alerts {
			found++
			for _, problem := range unsaid(a) {
				t.Error(problem + "\n\nWhoever reads an alert is owed what it is, what it means and what to try first, each in an annotation of its own. The channel shows the first step on a line by itself.")
			}
		}
	}
	if found == 0 {
		t.Fatal("no alert was found in what Flux applies, so this checked nothing")
	}

	// And the channel is shown it. An annotation nothing prints is a first
	// step nobody is given.
	path, doc := document(t, "HelmRelease", "kube-prometheus-stack")
	raw, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), ".Annotations.first_step") {
		t.Errorf("%s: what Alertmanager sends the channel does not print an alert's first step, so every alert's is written and none is read.", path)
	}
}

func TestAnAlertIsHeldToSayingAllThreeThings(t *testing.T) {
	alerts, err := alertsIn("f.yaml", `
kind: PrometheusRule
spec:
  groups:
    - rules:
        - record: a:recorded
          expr: vector(1)
        - alert: Whole
          annotations: {summary: It is down., description: Nothing answers., first_step: Start it.}
        - alert: NoStep
          annotations: {summary: It is down., description: Nothing answers.}
        - alert: StepSaysItAgain
          annotations: {summary: It is down., description: Nothing answers., first_step: It is down.}
        - alert: Bare
---
kind: ConfigMap
spec:
  groups:
    - rules:
        - alert: NotARule
`)
	if err != nil || len(alerts) != 4 {
		t.Fatalf("the alerts of a rule, and of nothing else, are four; got %d, %v", len(alerts), err)
	}
	for i, want := range [][]string{nil, {"the first thing to try"}, {"what it has already said"}, {"what it is", "what it means", "the first thing to try"}} {
		got := unsaid(alerts[i])
		if len(got) != len(want) {
			t.Errorf("%s: want %d thing(s) unsaid, got %v", alerts[i].name, len(want), got)
			continue
		}
		for _, w := range want {
			if !strings.Contains(strings.Join(got, "\n"), w) {
				t.Errorf("%s: nothing says %q: %v", alerts[i].name, w, got)
			}
		}
	}
	if _, err := alertsIn("f.yaml", "kind: {never closed"); err == nil {
		t.Error("a manifest that cannot be read was read as holding no alert")
	}
}
