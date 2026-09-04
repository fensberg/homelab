package main

import (
	"encoding/json"
	"fmt"
)

// Findings as SARIF, so each one can be dismissed on its own.
//
// The alternative was one comment holding all of them as prose. Code scanning
// renders each finding where it is, with a dismiss control and three stated
// reasons - false positive, won't fix, used in tests. That last part is worth
// more than the presentation: this epoch's acceptance test is whether the
// clerk is decorative, and "false positive" against "won't fix" is exactly the
// split between the clerk being wrong and the clerk being right and overruled.
// A judgement call becomes a count.
//
// Two constraints come with it. Every result is `note`, never `error`, because
// the clerk has no lever and a severity that fails a check would be one. And
// the code scanning results check must stay out of the required checks on
// main, for the same reason - a rule this file cannot enforce, recorded in the
// epoch and in the workflow that uploads this.

const toolURI = "https://github.com/fensberg/homelab/tree/main/scripts/clerk"

func sarif(found []snag) ([]byte, error) {
	rules := []any{
		rule(ruleUnsound, "The work is unsound",
			"Something that does not hold together: a part nothing reaches, a call that goes nowhere, a value written and read by nobody else, a tangle."),
		rule(ruleDisagrees, "The commentary disagrees with the code",
			"A comment, doc string or record claims something the code does not do. Found by asking a reader that never saw the commentary what the code does, and only then showing it the claim."),
	}

	results := make([]any, 0, len(found))
	for _, s := range found {
		results = append(results, map[string]any{
			"ruleId": s.Rule,
			// Advisory, always. The clerk is an outside party.
			"level":   "note",
			"message": map[string]any{"text": s.Message},
			"locations": []any{map[string]any{
				"physicalLocation": map[string]any{
					"artifactLocation": map[string]any{"uri": s.Path},
					"region":           map[string]any{"startLine": s.Line},
				},
			}},
			"partialFingerprints": map[string]any{
				"clerkFinding/v1": s.fingerprint(),
			},
		})
	}

	doc := map[string]any{
		"$schema": "https://json.schemastore.org/sarif-2.1.0.json",
		"version": "2.1.0",
		"runs": []any{map[string]any{
			"tool": map[string]any{"driver": map[string]any{
				"name":           "clerk",
				"informationUri": toolURI,
				"rules":          rules,
			}},
			"results": results,
		}},
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("building the SARIF report: %w", err)
	}
	return out, nil
}

func rule(id, short, full string) any {
	return map[string]any{
		"id":               id,
		"shortDescription": map[string]any{"text": short},
		"fullDescription":  map[string]any{"text": full},
		"defaultConfiguration": map[string]any{
			"level": "note",
		},
	}
}
