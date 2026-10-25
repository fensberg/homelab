package repo

import (
	"bytes"
	"errors"
	"io"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"homelab/details/capacity"
	supplierslist "homelab/details/suppliers"
)

// Each pool of pull-request runners reaches what its jobs are declared to
// reach, and what any runner needs to be a runner, and nothing else.
//
// WHY. A pull request's job on the estate's own runners is held by the
// cluster's network and by nothing in the runner. Which job may reach what
// is declared once, in the suppliers list, with the pool each job is of. A
// pool's policy is that list written out as the network reads it, so the
// two are one fact in two places, and this is what stops them parting: a
// host added to a policy that no job declared, or a job given a host its
// pool's policy never got, which then fails a long way from here.
//
// IT ALSO HOLDS EACH POOL TO BEGINNING FROM NOTHING: a policy for every
// pool, on 443 and nothing else, with the resolver held to the same names;
// nothing let in; everything in the namespace refused until a policy says
// otherwise; no token of the cluster's in a runner; and every runner sent
// to an untrusted zone's machine and able to stay there.
//
// FOUND BY WHAT IT IS. The policies are whatever policies select a pool's
// runners, wherever they are declared.

// poolLabel is the label the controller puts on a runner, which is the name
// of the set it is of and the only thing that tells one pool's from another.
const poolLabel = "actions.github.com/scale-set-name"

// poolSet is the name a pool's runners register under, as a manifest every
// site reconciles writes it.
func poolSet(pool string) string { return "pr-" + pool + "-${SITE}" }

// reaches is what one pool is declared to reach: every runner's hosts and
// its own jobs', without the port.
func reaches(pools supplierslist.Pools, egress []supplierslist.Egress, pool string) []string {
	set := map[string]bool{}
	for _, h := range pools.Runner {
		set[strings.TrimSuffix(h, ":443")] = true
	}
	for _, e := range egress {
		if e.Pool != pool {
			continue
		}
		for _, h := range e.Hosts {
			set[strings.TrimSuffix(h, ":443")] = true
		}
	}
	out := make([]string, 0, len(set))
	for h := range set {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// documents is every YAML document in a body that is a mapping.
func documents(body []byte) ([]map[string]any, error) {
	var out []map[string]any
	dec := yaml.NewDecoder(bytes.NewReader(body))
	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if doc != nil {
			out = append(out, doc)
		}
	}
}

// at walks a decoded document by keys.
func walk(v any, keys ...string) any {
	for _, k := range keys {
		m, _ := v.(map[string]any)
		v = m[k]
	}
	return v
}

// named is the hosts a list of name rules names: matchName, or
// matchPattern for a name with a wildcard in it.
func namedBy(rules any) []string {
	var out []string
	list, _ := rules.([]any)
	for _, r := range list {
		rule, _ := r.(map[string]any)
		for _, key := range []string{"matchName", "matchPattern"} {
			if v, ok := rule[key].(string); ok {
				out = append(out, v)
			}
		}
	}
	sort.Strings(out)
	return out
}

// poolPolicyProblems is everything wrong with the policy that holds one
// pool, given what the pool is declared to reach.
func poolPolicyProblems(policy map[string]any, want []string) []string {
	var out []string
	if in := walk(policy, "spec", "ingress"); in != nil {
		out = append(out, "it lets something in: a runner is reached by nothing")
	}
	egress, _ := walk(policy, "spec", "egress").([]any)
	var toNames, asked []string
	sawNames, sawResolver := false, false
	for _, e := range egress {
		rule, _ := e.(map[string]any)
		ports, _ := walk(rule, "toPorts").([]any)
		switch {
		case rule["toFQDNs"] != nil:
			sawNames = true
			toNames = append(toNames, namedBy(rule["toFQDNs"])...)
			for _, p := range ports {
				each, _ := walk(p, "ports").([]any)
				for _, one := range each {
					port, _ := one.(map[string]any)
					if port["port"] != "443" || port["protocol"] != "TCP" {
						out = append(out, "it opens a port that is not 443/TCP to the hosts it names")
					}
				}
			}
			if len(ports) == 0 {
				out = append(out, "it names hosts and no port, which is every port")
			}
		case rule["toEndpoints"] != nil:
			sawResolver = true
			for _, p := range ports {
				asked = append(asked, namedBy(walk(p, "rules", "dns"))...)
			}
		default:
			out = append(out, "it has a rule that is neither the resolver nor hosts by name: addresses, or everything")
		}
	}
	if !sawNames || !sawResolver {
		out = append(out, "it does not have both a rule for the resolver and a rule for hosts by name")
	}
	sort.Strings(toNames)
	sort.Strings(asked)
	if strings.Join(toNames, " ") != strings.Join(want, " ") {
		out = append(out, "it reaches\n      "+strings.Join(toNames, " ")+"\n    and its jobs and every runner are declared to reach\n      "+strings.Join(want, " "))
	}
	if strings.Join(asked, " ") != strings.Join(want, " ") {
		out = append(out, "its runners may ask the resolver for\n      "+strings.Join(asked, " ")+"\n    which is not the hosts they may reach: a name that can be asked for and not reached is a way to send something out inside a question")
	}
	return out
}

// runnerProblems is everything about a pool's own runners that lets them be
// more than a pull request's: a token of the cluster's, or anywhere to run
// but an untrusted zone's machine.
func runnerProblems(release map[string]any) []string {
	var out []string
	spec := walk(release, "spec", "values", "template", "spec")
	if walk(spec, "automountServiceAccountToken") != false {
		out = append(out, "its runners are given an account's token: automountServiceAccountToken is not false")
	}
	selector, _ := walk(spec, capacity.NodeSelectorKey).(map[string]any)
	zone, sent := selector[capacity.UntrustedZone].(string)
	if !sent || zone == "" {
		out = append(out, "its runners are not sent to an untrusted zone's machine: no nodeSelector for untrusted-zone, so they may land on a machine the estate trusts")
	}
	tolerated := false
	tolerations, _ := walk(spec, "tolerations").([]any)
	for _, tol := range tolerations {
		each, _ := tol.(map[string]any)
		tolerated = tolerated || (each["key"] == capacity.UntrustedZone && each["value"] == zone && sent)
	}
	if !tolerated {
		out = append(out, "its runners do not tolerate the zone's taint, so none can be placed and every job waits")
	}
	return out
}

func TestEachPoolOfPullRequestRunnersReachesWhatItsJobsDoAndNoMore(t *testing.T) {
	t.Parallel()
	tree := repository(t)
	pools, egress := tree.Pools(t), tree.Egress(t)
	if len(pools.Names) == 0 {
		t.Fatal("the suppliers list declares no pool of pull-request runners, so this checked nothing")
	}

	// The pools there are, said three times: in the list, to the guard that
	// lets a pull request's job use one, and by the jobs that are of one.
	declared := map[string]bool{}
	for _, name := range pools.Names {
		declared[name] = true
		if !pullRequestPools[pullRequestPool(name)] {
			t.Errorf("the suppliers list declares the pool %q, and it is not one a pull request's job may use (pullRequestPools)", name)
		}
	}
	if len(pullRequestPools) != len(pools.Names) {
		t.Errorf("a pull request's job may use %d pool(s) and the suppliers list declares %d", len(pullRequestPools), len(pools.Names))
	}
	for _, e := range egress {
		if e.Pool != "" && !declared[e.Pool] {
			t.Errorf("%s says it is of the pool %q, which the suppliers list does not declare", e.Job, e.Pool)
		}
	}

	// Every policy that selects a pool's runners, every release that makes
	// a pool, and every namespace that refuses everything, wherever each is.
	policies := map[string][]map[string]any{}
	releases := map[string]map[string]any{}
	namespaceOf := map[string]string{}
	closed := map[string]bool{}
	for _, rel := range tree.Files(t) {
		if !authoredHere(t, rel) || !strings.HasPrefix(rel, "clusters/") || !strings.HasSuffix(rel, ".yaml") {
			continue
		}
		for _, doc := range mappings(t, tree, rel) {
			namespace, _ := walk(doc, "metadata", "namespace").(string)
			switch doc["kind"] {
			case "CiliumNetworkPolicy":
				if set, ok := walk(doc, "spec", "endpointSelector", "matchLabels", poolLabel).(string); ok {
					policies[set] = append(policies[set], doc)
				}
			case "HelmRelease":
				if set, ok := walk(doc, "spec", "values", "runnerScaleSetName").(string); ok {
					releases[set] = doc
					namespaceOf[set] = namespace
				}
			case "NetworkPolicy":
				selector, isMap := walk(doc, "spec", "podSelector").(map[string]any)
				types, _ := walk(doc, "spec", "policyTypes").([]any)
				if isMap && len(selector) == 0 && len(types) == 2 && walk(doc, "spec", "ingress") == nil && walk(doc, "spec", "egress") == nil {
					closed[namespace] = true
				}
			}
		}
	}

	for _, pool := range pools.Names {
		set := poolSet(pool)
		release, made := releases[set]
		if !made {
			t.Errorf("the pool %q is declared and no release makes a set of runners named %s", pool, set)
			continue
		}
		for _, problem := range runnerProblems(release) {
			t.Errorf("the pool %q: %s", pool, problem)
		}
		if !closed[namespaceOf[set]] {
			t.Errorf("the pool %q is in the namespace %q, where nothing refuses everything first: a runner no policy selects reaches whatever it likes", pool, namespaceOf[set])
		}
		if len(policies[set]) != 1 {
			t.Errorf("the pool %q has %d policies selecting its runners, and is held by exactly one", pool, len(policies[set]))
			continue
		}
		for _, problem := range poolPolicyProblems(policies[set][0], reaches(pools, egress, pool)) {
			t.Errorf("the pool %q: %s", pool, problem)
		}
	}
}

// The judging, on policies and runners made up here.
func TestAPoolsPolicyIsHeldToItsListAndItsRunnersToNothingOfTheClusters(t *testing.T) {
	t.Parallel()
	const good = `
spec:
  egress:
    - toEndpoints: [{matchLabels: {app: resolver}}]
      toPorts:
        - ports: [{port: "53", protocol: UDP}]
          rules: {dns: [{matchName: a.example.invalid}, {matchPattern: "*.b.example.invalid"}]}
    - toFQDNs: [{matchName: a.example.invalid}, {matchPattern: "*.b.example.invalid"}]
      toPorts: [{ports: [{port: "443", protocol: TCP}]}]
`
	want := reaches(supplierslist.Pools{Runner: []string{"*.b.example.invalid:443"}},
		[]supplierslist.Egress{{Pool: "one", Hosts: []string{"a.example.invalid:443"}}, {Pool: "other", Hosts: []string{"c.example.invalid:443"}}}, "one")
	if strings.Join(want, " ") != "*.b.example.invalid a.example.invalid" {
		t.Fatalf("what the pool reaches was worked out as %v", want)
	}
	for name, c := range map[string]struct {
		change func(string) string
		says   string
	}{
		"its list and no more": {func(s string) string { return s }, ""},
		"a host nobody declared": {func(s string) string {
			return strings.Replace(s, "toFQDNs: [", "toFQDNs: [{matchName: extra.example.invalid}, ", 1)
		}, "are declared to reach"},
		"a declared host left out": {func(s string) string {
			return strings.Replace(s, "toFQDNs: [{matchName: a.example.invalid}, ", "toFQDNs: [", 1)
		}, "are declared to reach"},
		"any name asked for": {func(s string) string {
			return strings.Replace(s, "dns: [{matchName: a.example.invalid}, ", "dns: [{matchPattern: \"*\"}, ", 1)
		}, "inside a question"},
		"another port": {func(s string) string { return strings.Replace(s, `port: "443"`, `port: "80"`, 1) }, "not 443/TCP"},
		"every port": {func(s string) string {
			return strings.Replace(s, "\n      toPorts: [{ports: [{port: \"443\", protocol: TCP}]}]", "", 1)
		}, "which is every port"},
		"something let in":       {func(s string) string { return s + "  ingress: [{}]\n" }, "lets something in"},
		"an address, not a name": {func(s string) string { return s + "    - toCIDR: [\"192.0.2.0/24\"]\n" }, "neither the resolver nor hosts by name"},
	} {
		docs, err := documents([]byte(c.change(good)))
		if err != nil || len(docs) != 1 {
			t.Fatalf("%s: %v", name, err)
		}
		said := strings.Join(poolPolicyProblems(docs[0], want), "\n")
		if (c.says == "") != (said == "") || !strings.Contains(said, c.says) {
			t.Errorf("%s: want %q, got\n%s", name, c.says, said)
		}
	}

	const runner = `
spec:
  values:
    template:
      spec:
        automountServiceAccountToken: false
        nodeSelector: {untrusted-zone: somewhere}
        tolerations: [{key: untrusted-zone, value: somewhere, effect: NoSchedule}]
`
	for name, c := range map[string]struct {
		change func(string) string
		says   string
	}{
		"held":    {func(s string) string { return s }, ""},
		"a token": {func(s string) string { return strings.Replace(s, "Token: false", "Token: true", 1) }, "given an account's token"},
		"a token by default": {func(s string) string {
			return strings.Replace(s, "        automountServiceAccountToken: false\n", "", 1)
		}, "given an account's token"},
		"sent nowhere": {func(s string) string {
			return strings.Replace(s, "        nodeSelector: {untrusted-zone: somewhere}\n", "", 1)
		}, "may land on a machine the estate trusts"},
		"tolerating another zone": {func(s string) string { return strings.Replace(s, "value: somewhere", "value: elsewhere", 1) }, "do not tolerate the zone's taint"},
	} {
		docs, err := documents([]byte(c.change(runner)))
		if err != nil || len(docs) != 1 {
			t.Fatalf("%s: %v", name, err)
		}
		said := strings.Join(runnerProblems(docs[0]), "\n")
		if (c.says == "") != (said == "") || !strings.Contains(said, c.says) {
			t.Errorf("%s: want %q, got\n%s", name, c.says, said)
		}
	}
	if _, err := documents([]byte("a: [unclosed")); err == nil {
		t.Error("YAML that does not parse was read as holding nothing")
	}
}
