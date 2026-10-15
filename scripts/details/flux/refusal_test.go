package flux

import (
	"homelab/details/capacity"
	"strings"
	"testing"
)

// What was refused is said as the object, the field and the rule it broke,
// in either shape the cluster says it in; and a value the message quotes is
// never said, whatever it is.
func TestARefusalIsSaidWithoutTheValuesItQuotes(t *testing.T) {
	for name, c := range map[string]struct{ message, want string }{
		"as Flux says it": {
			`PriorityClass/interactive dry-run failed (Invalid): PriorityClass.scheduling.k8s.io "interactive" is invalid: preemptionPolicy: Invalid value: "PreemptLowerPriority": field is immutable`,
			"PriorityClass/interactive: preemptionPolicy: field is immutable",
		},
		"as the API server says it": {
			`The PriorityClass "interactive" is invalid: preemptionPolicy: Invalid value: "PreemptLowerPriority": field is immutable`,
			"PriorityClass/interactive: preemptionPolicy: field is immutable",
		},
		"two fields, in a namespace": {
			`StatefulSet/books/ledger dry-run failed (Invalid): StatefulSet.apps "ledger" is invalid: [spec.selector: Invalid value: "a-quoted-secret-looking-value": field is immutable, spec.volumeClaimTemplates[0].spec: Forbidden: updates to statefulset spec for fields other than 'replicas' are forbidden]`,
			"StatefulSet/books/ledger: spec.selector: field is immutable; spec.volumeClaimTemplates[0].spec: updates to statefulset spec for fields other than 'replicas' are forbidden",
		},
		"a number": {
			`The PriorityClass "batch" is invalid: value: Invalid value: 100: field is immutable`,
			"PriorityClass/batch: value: field is immutable",
		},
		"an object and no field it can read":     {`PriorityClass/batch dry-run failed (Conflict): the object has been modified`, "PriorityClass/batch: refused"},
		"nothing it can read":                    {`connection reset by peer while talking to 10.1.2.3`, "a refusal this does not read; the cluster's own message says what it was"},
		"a value with a quote escaped inside it": {`The Secret "s" is invalid: data: Invalid value: "a\"b,c": too long`, "Secret/s: data: too long"},
	} {
		if got := Refusal(c.message); got != c.want {
			t.Errorf("%s:\n got  %q\n want %q", name, got, c.want)
		}
		for _, secret := range []string{capacity.PreemptsLower, "a-quoted-secret-looking-value", "10.1.2.3", `a\"b`, "100"} {
			if strings.Contains(c.message, secret) && strings.Contains(Refusal(c.message), secret) {
				t.Errorf("%s: the value %q was said", name, secret)
			}
		}
	}
}
