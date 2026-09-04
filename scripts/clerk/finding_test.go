package main

import (
	"encoding/json"
	"strings"
	"testing"
)

var wasRead = map[string]int{"scripts/clerk/llm.go": 160, "docs/epochs/01.md": 40}

// The citation rule, enforced rather than requested.
//
// Each case is a finding that reads perfectly well and cannot be checked. That
// is the expensive kind: it costs the operator real time to chase, and this
// estate rejected reviewers whose wrong findings cost more than their right
// ones.
func TestUncheckableFindingsAreDropped(t *testing.T) {
	cases := []struct {
		name string
		in   snag
		why  string
	}{
		{"a file nobody read", snag{ruleUnsound, "scripts/other.go", 3, "tangled"}, "not read"},
		{"past the end of the file", snag{ruleUnsound, "scripts/clerk/llm.go", 9000, "tangled"}, "outside the file"},
		{"line zero", snag{ruleUnsound, "scripts/clerk/llm.go", 0, "tangled"}, "outside the file"},
		{"a rule it invented", snag{"style-nit", "scripts/clerk/llm.go", 3, "ugly"}, "unknown rule"},
		{"nothing said", snag{ruleUnsound, "scripts/clerk/llm.go", 3, "  "}, "no message"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kept, dropped := keep([]snag{c.in}, wasRead)
			if len(kept) != 0 {
				t.Fatalf("kept an uncheckable finding: %+v", kept)
			}
			if len(dropped) != 1 || !strings.Contains(dropped[0], c.why) {
				t.Errorf("dropped reason %q should mention %q", dropped, c.why)
			}
		})
	}
}

func TestACheckableFindingSurvives(t *testing.T) {
	in := snag{ruleDisagrees, "docs/epochs/01.md", 12, "claims the button is idempotent; nothing here retries"}
	kept, dropped := keep([]snag{in}, wasRead)
	if len(kept) != 1 || len(dropped) != 0 {
		t.Fatalf("kept=%v dropped=%v", kept, dropped)
	}
}

// The same defect said twice is one snag.
func TestTheSameFindingTwiceIsReportedOnce(t *testing.T) {
	one := snag{ruleUnsound, "scripts/clerk/llm.go", 10, "nothing reaches this"}
	two := snag{ruleUnsound, "scripts/clerk/llm.go", 44, "nothing reaches this"}
	kept, dropped := keep([]snag{one, two}, wasRead)
	if len(kept) != 1 {
		t.Errorf("kept %d, want the duplicate collapsed", len(kept))
	}
	if len(dropped) != 1 || !strings.Contains(dropped[0], "repeats") {
		t.Errorf("the duplicate was dropped without saying so: %v", dropped)
	}
}

// Dropping quietly would be its own blind spot.
//
// "Everything checked out" and "I discarded eleven findings I could not
// verify" are different facts, and only one of them is ever reassuring.
func TestWhatWasDroppedIsReportedRatherThanSwallowed(t *testing.T) {
	_, dropped := keep([]snag{
		{ruleUnsound, "nope.go", 1, "x"},
		{"invented", "scripts/clerk/llm.go", 1, "y"},
	}, wasRead)
	if len(dropped) != 2 {
		t.Fatalf("dropped %d reasons, want one per discarded finding", len(dropped))
	}
}

// Models fence their JSON however firmly they are asked not to.
func TestParseSurvivesACodeFence(t *testing.T) {
	answer := "Here is what I found:\n\n```json\n[{\"rule\":\"unsound-work\",\"path\":\"a.go\",\"line\":2,\"message\":\"m\"}]\n```\n"
	found, err := parse(answer)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(found) != 1 || found[0].Path != "a.go" {
		t.Errorf("got %+v", found)
	}
}

// An unreadable answer is an error, never an empty list.
//
// No findings and could-not-read must not look the same. One means the work is
// sound; the other means nobody looked.
func TestAnUnreadableAnswerIsAnErrorNotAnEmptyList(t *testing.T) {
	if _, err := parse("I could not do that."); err == nil {
		t.Fatal("prose was accepted as a finding-free run")
	}
}

// A finding that shifts down the file is the same finding.
func TestFingerprintIgnoresTheLine(t *testing.T) {
	a := snag{ruleUnsound, "a.go", 10, "nothing reaches this"}
	b := snag{ruleUnsound, "a.go", 84, "nothing reaches this"}
	if a.fingerprint() != b.fingerprint() {
		t.Error("an edit above the finding would open a second alert for it")
	}
	c := snag{ruleUnsound, "a.go", 10, "something else"}
	if a.fingerprint() == c.fingerprint() {
		t.Error("two different findings share a fingerprint")
	}
}

// The no-lever guarantee, at the output.
//
// A SARIF result at error level fails the code scanning check, and a failing
// check is the lever this whole design removes from the outside party.
func TestEverySarifResultIsAdvisory(t *testing.T) {
	out, err := sarif([]snag{
		{ruleUnsound, "a.go", 1, "one"},
		{ruleDisagrees, "b.md", 2, "two"},
	})
	if err != nil {
		t.Fatalf("sarif: %v", err)
	}

	var doc struct {
		Version string `json:"version"`
		Runs    []struct {
			Tool struct {
				Driver struct {
					Name  string `json:"name"`
					Rules []struct {
						ID                   string `json:"id"`
						DefaultConfiguration struct {
							Level string `json:"level"`
						} `json:"defaultConfiguration"`
					} `json:"rules"`
				} `json:"driver"`
			} `json:"tool"`
			Results []struct {
				Level     string `json:"level"`
				Locations []struct {
					PhysicalLocation struct {
						ArtifactLocation struct {
							URI string `json:"uri"`
						} `json:"artifactLocation"`
						Region struct {
							StartLine int `json:"startLine"`
						} `json:"region"`
					} `json:"physicalLocation"`
				} `json:"locations"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("the report is not valid JSON: %v", err)
	}
	if doc.Version != "2.1.0" || len(doc.Runs) != 1 {
		t.Fatalf("version=%q runs=%d", doc.Version, len(doc.Runs))
	}
	if doc.Runs[0].Tool.Driver.Name != "clerk" {
		t.Errorf("tool is %q; alerts are grouped by it", doc.Runs[0].Tool.Driver.Name)
	}
	for _, r := range doc.Runs[0].Results {
		if r.Level != "note" {
			t.Errorf("a result is %q; the clerk may not raise anything that fails a check", r.Level)
		}
		if len(r.Locations) != 1 || r.Locations[0].PhysicalLocation.Region.StartLine == 0 {
			t.Error("a result has no location; an uncited finding must not reach the report")
		}
	}
	for _, rl := range doc.Runs[0].Tool.Driver.Rules {
		if rl.DefaultConfiguration.Level != "note" {
			t.Errorf("rule %s defaults to %q", rl.ID, rl.DefaultConfiguration.Level)
		}
	}
}
