package flux

import (
	"regexp"
	"strings"
)

// Refusal is what the cluster refused and why, from the message it refused
// with, and nothing else of it: the object, each field that was refused and
// the rule it broke. A value the message repeats is dropped.
//
// It is for a log the public can read - the converge's, a pull request's
// plan - where what the API server says has to be said and a value it
// quotes may be the estate's own. So it is built up from what is known to be
// structure, rather than cut down from the message: anything it does not
// recognise is left out, and an object it cannot name is said as such.
//
// The two shapes it reads are Flux's, "PriorityClass/interactive dry-run
// failed (Invalid): ...", and the API server's own, "The PriorityClass
// "interactive" is invalid: ...". After either, each refused field is
// "field: Kind of error[: "value"]: what was wrong", separated by commas.
func Refusal(message string) string {
	object := ""
	if m := fluxObject.FindStringSubmatch(message); m != nil {
		object = m[1]
	} else if m := apiObject.FindStringSubmatch(message); m != nil {
		object = m[1] + "/" + m[2]
	}
	var fields []string
	for _, m := range refusedField.FindAllStringSubmatch(message, -1) {
		fields = append(fields, m[1]+": "+strings.TrimSpace(m[3]))
	}
	switch {
	case object == "" && len(fields) == 0:
		return "a refusal this does not read; the cluster's own message says what it was"
	case object == "":
		object = "an object"
	}
	if len(fields) == 0 {
		return object + ": refused"
	}
	return object + ": " + strings.Join(fields, "; ")
}

var (
	// An object as Flux names it: Kind/name, or Kind/namespace/name.
	fluxObject = regexp.MustCompile(`^([A-Za-z]+(?:/[a-z0-9.-]+){1,2}) dry-run failed`)
	// An object as the API server names it.
	apiObject = regexp.MustCompile(`The ([A-Za-z]+) "([a-z0-9.-]+)" is invalid`)
	// A refused field, the rule it broke, and the value it quotes, which is
	// matched only to be left out. What was wrong ends at a comma, a closing
	// bracket or the end of the message.
	refusedField = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9]*(?:\[[^\]]*\])?(?:\.[a-zA-Z][a-zA-Z0-9]*(?:\[[^\]]*\])?)*): (?:Invalid value|Unsupported value|Duplicate value|Required value|Forbidden|Not found|Too long|Too many)(: "(?:[^"\\]|\\.)*"|: [0-9]+)?: ([^,\]\n]+)`)
)
