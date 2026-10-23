package repo

import (
	"fmt"
	"strings"
	"testing"
)

// The cluster refuses a pod it cannot count, and this is whether that refusal
// has a way round it.
//
// What the rule asks of a pod is proved against a real cluster by the
// integration tier, which offers it pods and reads the answer. That cannot
// see a rule that is right and applies to less than everything: a namespace
// left out, a label that excuses a pod, a refusal turned down to a warning.
// Each of those is one line, reads as configuration, and makes the site's sum
// untrue for exactly the pods nobody is looking at.
func TestNoPodIsLetPastTheRefusalOfUncountedWork(t *testing.T) {
	// Found by the policy it declares; its binding is read from beside it.
	path, body := fluxObject(t, "ValidatingAdmissionPolicy", "uncounted-pods")

	specs := map[string]map[string]any{}
	for _, decoded := range body {
		var doc struct {
			Kind string         `yaml:"kind"`
			Spec map[string]any `yaml:"spec"`
		}
		if err := as(decoded, &doc); err != nil {
			t.Fatalf("%s has a document that does not read as an object: %v", path, err)
		}
		if doc.Kind != "" {
			specs[doc.Kind] = doc.Spec
		}
	}
	policy, binding := specs["ValidatingAdmissionPolicy"], specs["ValidatingAdmissionPolicyBinding"]
	if policy == nil || binding == nil {
		t.Fatalf("%s does not declare both the policy and its binding. A policy nothing binds refuses nothing.", path)
	}

	var wrong []string
	if got := fmt.Sprint(binding["validationActions"]); got != "[Deny]" {
		wrong = append(wrong, "the binding's validationActions are "+got+", not [Deny]: a pod that breaks the rule is told so and let in")
	}
	if got := fmt.Sprint(policy["failurePolicy"]); got != "Fail" {
		wrong = append(wrong, "the policy's failurePolicy is "+got+", not Fail: a rule that cannot be evaluated lets the pod in")
	}
	// Everything that narrows who the rule applies to. A pod is matched by
	// being a pod and by nothing else.
	for _, key := range sortedKeys(binding) {
		if key != "policyName" && key != "validationActions" {
			wrong = append(wrong, "the binding sets "+key+", which chooses which pods the rule applies to")
		}
	}
	if _, has := policy["matchConditions"]; has {
		wrong = append(wrong, "the policy sets matchConditions, which excuse a pod from being judged at all")
	}
	constraints, _ := policy["matchConstraints"].(map[string]any)
	for _, key := range sortedKeys(constraints) {
		if key != "resourceRules" {
			wrong = append(wrong, "the policy's matchConstraints set "+key+", which leaves some pods out")
		}
	}
	if !strings.Contains(fmt.Sprint(constraints["resourceRules"]), "pods") {
		wrong = append(wrong, "the policy's resourceRules do not name pods, so it judges nothing")
	}

	if len(wrong) > 0 {
		t.Fatalf(`%s lets a pod past the refusal of uncounted work:

  %s

The site's limit is a sum of what pods reserve. A pod let in without a
reservation or a priority class is one the sum does not hold, and a way out
for some pods is where such a pod will be put. If something cannot meet the
rule, give it a reservation and a class.`, path, strings.Join(wrong, "\n  "))
	}
}
