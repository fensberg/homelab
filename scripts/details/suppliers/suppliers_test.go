package suppliers

import (
	"strings"
	"testing"
)

const written = `# What may be taken, and from whom.
providers:
  - source: somebody/something
tools:
  - source: a-publisher/a-tool
    version: A_TOOL_VERSION
    fetch: "https://example.invalid/{version}/a-tool"   # quoted, with a comment after
    reason: >-
      Folded over
      two lines.
  - source: another/tool
    pypi: another-tool
egress:
  - job: a-workflow.yml/a-job
    policy: block
    hosts:
      - example.invalid:443
      - "*.example.invalid:443"
  - job: a-workflow.yml/watched
    policy: audit
    reason: it reaches hosts nobody can list
`

// A tool's entry is everything it says, by the name it says it under, read
// as YAML reads it: a quoted value without its quotes, a folded one as one
// line, a comment as nothing.
func TestAToolsEntryIsEverythingItSaysReadAsYAMLReadsIt(t *testing.T) {
	tools, err := Tools([]byte(written))
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Source != "a-publisher/a-tool" || tools[1].Source != "another/tool" {
		t.Fatalf("two tools were read as %+v", tools)
	}
	first := tools[0].Fields
	if first["version"] != "A_TOOL_VERSION" || first["fetch"] != "https://example.invalid/{version}/a-tool" || first["reason"] != "Folded over two lines." {
		t.Errorf("the first tool's fields were read as %v", first)
	}
	if tools[1].Fields["pypi"] != "another-tool" || len(tools[1].Fields) != 1 {
		t.Errorf("the second tool's fields were read as %v", tools[1].Fields)
	}
}

// A job's entry is its policy and its hosts, and a host is read whole.
func TestAJobsEntryIsItsPolicyAndItsHosts(t *testing.T) {
	egress, err := EgressOf([]byte(written))
	if err != nil {
		t.Fatal(err)
	}
	if len(egress) != 2 {
		t.Fatalf("two jobs were read as %+v", egress)
	}
	if egress[0].Job != "a-workflow.yml/a-job" || egress[0].Policy != "block" || strings.Join(egress[0].Hosts, " ") != "example.invalid:443 *.example.invalid:443" {
		t.Errorf("the first job was read as %+v", egress[0])
	}
	if egress[1].Policy != "audit" || egress[1].Reason != "it reaches hosts nobody can list" || len(egress[1].Hosts) != 0 {
		t.Errorf("the second job was read as %+v", egress[1])
	}
}

// What is not the list is refused: text that is not YAML, an entry that is
// not an entry, a tool with nobody supplying it.
func TestWhatIsNotTheListIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"not YAML":           "tools: [unclosed",
		"two documents":      "tools: []\n---\ntools: []\n",
		"an entry of text":   "tools:\n  - just words\n",
		"a tool of nobody's": "tools:\n  - version: X\n",
	} {
		if _, err := Tools([]byte(body)); err == nil {
			t.Errorf("%s was read as the list", name)
		}
	}
}

// A job says which pool of pull-request runners it is of, and the list says
// which pools there are and what a runner in any of them reaches.
func TestAJobIsOfAPoolAndThePoolsAreDeclared(t *testing.T) {
	const withPools = `
pull_request_pools:
  names: [one, two]
  every_runner_reaches:
    - example.invalid:443
    - "*.example.invalid:443"
egress:
  - job: a-workflow.yml/in-a-pool
    pool: one
    policy: block
    hosts: [example.invalid:443]
  - job: a-workflow.yml/in-none
    policy: block
`
	pools, err := PoolsOf([]byte(withPools))
	if err != nil || strings.Join(pools.Names, " ") != "one two" || strings.Join(pools.Runner, " ") != "example.invalid:443 *.example.invalid:443" {
		t.Errorf("the pools were read as %+v, %v", pools, err)
	}
	egress, err := EgressOf([]byte(withPools))
	if err != nil || len(egress) != 2 || egress[0].Pool != "one" || egress[1].Pool != "" {
		t.Errorf("the jobs were read as %+v, %v", egress, err)
	}

	if none, err := PoolsOf([]byte("egress: []\n")); err != nil || len(none.Names) != 0 {
		t.Errorf("a list that declares no pools was read as %+v, %v", none, err)
	}
	for name, body := range map[string]string{
		"a pool named twice":   "pull_request_pools:\n  names: [one, one]\n",
		"reaches, and no pool": "pull_request_pools:\n  every_runner_reaches: [example.invalid:443]\n",
		"not the list":         "pull_request_pools: [unclosed",
	} {
		if _, err := PoolsOf([]byte(body)); err == nil {
			t.Errorf("%s was read as pools", name)
		}
	}
}
