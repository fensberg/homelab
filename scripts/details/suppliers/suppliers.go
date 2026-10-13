// Package suppliers reads the approved suppliers list.
//
// The list (scripts/approved-suppliers.yml) says what the estate may take
// and from whom, and which job may reach what. Three programs read it, and
// each read it by matching its lines: a tool's entry was whatever followed
// "- source:" until the next one. They read it here now, once, from what the
// drafter redraws of it.
package suppliers

import (
	"fmt"

	"homelab/details/drafter"
)

// Path is where the list is, from the top of the repository.
const Path = "scripts/approved-suppliers.yml"

// PolicyKey is what a workflow's step is handed its egress policy under,
// which the list's own entries are compared with.
const PolicyKey = "egress-policy"

// Tool is one entry under tools: who supplies it, and everything else the
// entry says, by the name it says it under. What a field means is for
// whoever asks to say: one program orders deliveries and another checks
// them, and neither reads the other's fields.
type Tool struct {
	Source string
	Fields map[string]string
}

// Egress is one entry under egress: a job, by its workflow and its name,
// whether it is held to a list of hosts, and the list.
type Egress struct {
	Job    string
	Policy string
	Reason string
	Hosts  []string
	// Pool is the pool of the estate's pull-request runners the job is of,
	// and empty for a job that is of none.
	Pool string
}

// Pools is the estate's pools of pull-request runners: their names, and
// what a runner in any of them reaches to be a runner at all.
type Pools struct {
	Names  []string
	Runner []string
}

// PoolsOf is what the list declares under pull_request_pools:. A list that
// declares none has none; one that names a pool twice, or none at all while
// saying what a runner reaches, is refused.
func PoolsOf(body []byte) (Pools, error) {
	doc, err := list(body)
	if err != nil {
		return Pools{}, err
	}
	declared, _ := doc["pull_request_pools"].(map[string]any)
	var out Pools
	seen := map[string]bool{}
	names, _ := declared["names"].([]any)
	for _, n := range names {
		name := fmt.Sprint(n)
		if seen[name] {
			return Pools{}, fmt.Errorf("%s: pull_request_pools names the pool %q twice", Path, name)
		}
		seen[name] = true
		out.Names = append(out.Names, name)
	}
	reaches, _ := declared["every_runner_reaches"].([]any)
	for _, h := range reaches {
		out.Runner = append(out.Runner, fmt.Sprint(h))
	}
	if len(out.Names) == 0 && len(out.Runner) > 0 {
		return Pools{}, fmt.Errorf("%s: pull_request_pools says what a runner reaches and names no pool", Path)
	}
	return out, nil
}

// list is the one document the file holds.
func list(body []byte) (map[string]any, error) {
	docs, err := drafter.Documents(body)
	if err != nil {
		return nil, err
	}
	if len(docs) != 1 {
		return nil, fmt.Errorf("%s holds %d documents, and it is one list", Path, len(docs))
	}
	return docs[0], nil
}

// Tools is every entry under tools:, in the order written.
func Tools(body []byte) ([]Tool, error) {
	doc, err := list(body)
	if err != nil {
		return nil, err
	}
	entries, _ := doc["tools"].([]any)
	var out []Tool
	for i, e := range entries {
		entry, ok := e.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: entry %d under tools: is not a mapping", Path, i+1)
		}
		tool := Tool{Fields: map[string]string{}}
		for name, value := range entry {
			if name == "source" {
				tool.Source = fmt.Sprint(value)
				continue
			}
			if value != nil {
				tool.Fields[name] = fmt.Sprint(value)
			}
		}
		if tool.Source == "" {
			return nil, fmt.Errorf("%s: entry %d under tools: names no source", Path, i+1)
		}
		out = append(out, tool)
	}
	return out, nil
}

// EgressOf is every entry under egress:, in the order written.
func EgressOf(body []byte) ([]Egress, error) {
	doc, err := list(body)
	if err != nil {
		return nil, err
	}
	entries, _ := doc["egress"].([]any)
	var out []Egress
	for i, e := range entries {
		entry, ok := e.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: entry %d under egress: is not a mapping", Path, i+1)
		}
		one := Egress{}
		one.Job, _ = entry["job"].(string)
		one.Policy, _ = entry["policy"].(string)
		one.Reason, _ = entry["reason"].(string)
		one.Pool, _ = entry["pool"].(string)
		hosts, _ := entry["hosts"].([]any)
		for _, h := range hosts {
			one.Hosts = append(one.Hosts, fmt.Sprint(h))
		}
		out = append(out, one)
	}
	return out, nil
}
