package repo

import (
	"fmt"
	"strings"
	"testing"

	"homelab/details/applications"
	"homelab/details/privatenet"
)

// An application's egress policy may open private ranges, and never the one
// the estate lives in.
//
// WHY THIS IS A GUARD RATHER THAN A PARAGRAPH IN A MANIFEST. Which private
// ranges an application's policy permits is a PRODUCT decision, and the
// application's own: a game whose relay offers a player's local address as a
// direct-path candidate has to open the networks a player may sit on, and a
// player its policy denies joins, spawns, and is disconnected seconds later
// with nothing naming the policy (#392). That decision is tested where it is
// made, in the application's own tests.
//
// What is the estate's to refuse is the range that looks symmetrical with the
// two beside it: 10.0.0.0/8. It is the range this entire estate is addressed
// out of - node subnets at 10.<octet>.0.0/16, and each site's pods and
// services beside them. Opening it hands an application a path to the API
// server, the state database, the hypervisor and every other application -
// which is precisely what its policy exists to prevent.
//
// Nothing else would object. The manifest stays valid, kustomize builds it,
// Flux applies it, and the cluster does exactly what it was told. So the
// refusal lives here, for every application there is or will be, and
// tests/mutations.yml proves it fires.

const (
	// The estate's own space, and the reason it can never be a permitted peer.
	estateSpace = "10."
	estateRange = privatenet.Ten
	// Everywhere, which a policy may only permit with the estate taken out.
	everywhere = "0.0.0.0/0"
)

func TestNoApplicationCanBeGivenAPathIntoTheEstate(t *testing.T) {
	light(t)
	// EVERY policy that governs an application, not one file.
	//
	// NetworkPolicies are additive: a second one, anywhere in the repository,
	// selecting the same pods and permitting everything, opens the estate
	// without touching the file a narrower guard would read. That is not
	// hypothetical - it is exactly the shape a deliberate diagnostic took, and
	// the guard stayed green through it (#448).
	//
	// No floor on how many there are: an estate with no application has none,
	// and what this finds and refuses is proved against policies written
	// here, in TestPathsIntoTheEstateNamesEachWayOneIsOpened.
	for _, p := range applicationPolicies(t) {
		for _, problem := range pathsIntoTheEstate(p) {
			t.Error(problem)
		}
	}
}

// pathsIntoTheEstate is every way one policy gives what it governs a path
// into the estate's own address space.
func pathsIntoTheEstate(p applicationPolicy) []string {
	var problems []string
	if p.openEgress {
		problems = append(problems, fmt.Sprintf(`%s permits egress to everything.

An empty egress rule is every address, which includes %s - the space this
whole estate is addressed out of. An application with a path to the API
server, the state database and the hypervisor is the single thing its policy
exists to prevent, and a second policy saying so quietly is worse than the
first one saying it out loud.`, p.file, estateRange))
	}
	permitted, excepted, everywhereGranted := permittedPeers(p.egress)
	for _, cidr := range permitted {
		if strings.HasPrefix(cidr, estateSpace) {
			problems = append(problems, fmt.Sprintf(`%s permits egress to %s.

That is inside %s, which is the space this entire estate is addressed out of -
node subnets at 10.<octet>.0.0/16, and each site's pods and services (the
address plan's).

Permitting an application a path into that range gives a compromised one reach
to the API server, the state database, the hypervisor and every other
application, which is the single thing its policy exists to prevent.

If somebody on a 10.x network cannot get a direct path, that is the correct
outcome. The isolation boundary wins.`, p.file, cidr, estateRange))
		}
	}
	// And a rule for everywhere must subtract it, or the grant arrives from
	// the other direction with nothing above to notice.
	if everywhereGranted && !contains(excepted, estateRange) {
		problems = append(problems, fmt.Sprintf(`%s permits %s and does not exclude %s.

Everywhere, minus the estate, is the internet. Without that exclusion it is
every address in the estate, with no rule appearing to grant anything - the
change is a deleted line, and the diff is three characters shorter.

Excluded today: %s`, p.file, everywhere, estateRange, strings.Join(excepted, ", ")))
	}
	return problems
}

// The refusal is held to what it claims, against policies written here: one
// that opens the internet minus every private range and two private ranges
// beside it is accepted, and each way of opening the estate is named.
func TestPathsIntoTheEstateNamesEachWayOneIsOpened(t *testing.T) {
	light(t)
	const broad = "    - to:\n        - ipBlock:\n            cidr: 0.0.0.0/0\n            except:\n              - 10.0.0.0/8\n              - 172.20.0.0/16\n"
	for name, c := range map[string]struct {
		egress string
		want   string
	}{
		"the internet and two private ranges": {broad + "    - to:\n        - ipBlock:\n            cidr: 192.168.7.0/24\n        - ipBlock:\n            cidr: 172.20.0.0/16\n", ""},
		"no addresses at all":                 {"  egress:\n    - to:\n        - namespaceSelector: {}\n", ""},
		"the estate's own range":              {broad + "    - to:\n        - ipBlock:\n            cidr: 10.0.0.0/8\n", "permits egress to 10.0.0.0/8"},
		"a part of the estate's range":        {broad + "    - to:\n        - ipBlock:\n            cidr: 10.10.0.0/16\n", "permits egress to 10.10.0.0/16"},
		"everywhere, the estate included":     {"    - to:\n        - ipBlock:\n            cidr: 0.0.0.0/0\n            except:\n              - 172.20.0.0/16\n", "does not exclude 10.0.0.0/8"},
		"everywhere with no exception":        {"    - to:\n        - ipBlock:\n            cidr: 0.0.0.0/0\n", "does not exclude 10.0.0.0/8"},
		"an empty egress rule":                {"  egress:\n    - {}\n", "permits egress to everything"},
	} {
		problems := pathsIntoTheEstate(fixturePolicy(t, c.egress))
		switch {
		case c.want == "" && len(problems) != 0:
			t.Errorf("%s: refused: %v", name, problems)
		case c.want != "" && (len(problems) != 1 || !strings.Contains(problems[0], c.want)):
			t.Errorf("%s: want one problem saying %q, got %v", name, c.want, problems)
		}
	}
}

// permittedPeers splits the CIDRs of a policy's egress rules into what it
// grants and what it subtracts from a broad rule, and says whether one of
// them grants everywhere. An except entry and a permitted cidr are kept
// apart by where they are in the rule, so the broad internet rule is never
// read as a grant of the private ranges it takes away.
func permittedPeers(egress []map[string]any) (permitted, excepted []string, everywhereGranted bool) {
	for _, rule := range egress {
		peers, _ := rule["to"].([]any)
		for _, peer := range peers {
			m, _ := peer.(map[string]any)
			block, _ := m["ipBlock"].(map[string]any)
			cidr, _ := block["cidr"].(string)
			switch {
			case cidr == everywhere:
				everywhereGranted = true
			case cidr != "":
				permitted = append(permitted, cidr)
			}
			except, _ := block["except"].([]any)
			for _, e := range except {
				if v, ok := e.(string); ok {
					excepted = append(excepted, v)
				}
			}
		}
	}
	return permitted, excepted, everywhereGranted
}

// policyOf is a policy's egress rules, and whether one of them is empty -
// which is every address.
func policyOf(file string, egress []map[string]any) applicationPolicy {
	open := false
	for _, rule := range egress {
		if len(rule) == 0 {
			open = true
		}
	}
	return applicationPolicy{file: file, egress: egress, openEgress: open}
}

// fixturePolicy is a policy whose egress rules a test writes out, under
// spec.egress.
func fixturePolicy(t *testing.T, text string) applicationPolicy {
	t.Helper()
	if !strings.HasPrefix(strings.TrimSpace(text), "egress:") {
		text = "  egress:\n" + text
	}
	var doc struct {
		Spec struct {
			Egress []map[string]any `yaml:"egress"`
		} `yaml:"spec"`
	}
	docs := fixtureDocuments(t, "spec:\n"+text)
	if len(docs) != 1 || as(docs[0], &doc) != nil {
		t.Fatalf("a fixture policy is not one document of egress rules:\n%s", text)
	}
	return policyOf("p.yaml", doc.Spec.Egress)
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// A NetworkPolicy that governs an application, with its egress rules and
// whether they are unrestricted.
type applicationPolicy struct {
	file       string
	egress     []map[string]any
	openEgress bool
}

// applicationPolicies is every NetworkPolicy in the repository that governs
// an application: one written in an application's directory, and one written
// anywhere else that names an application's namespace.
//
// The whole repository rather than a named file. Policies are additive and a
// namespace can hold as many as anybody writes, so the question "what may
// this application reach" is answered by their union. A guard that reads one
// path answers a narrower question that looks identical until somebody adds a
// second file.
func applicationPolicies(t *testing.T) []applicationPolicy {
	t.Helper()
	tree := repository(t)
	namespaces := map[string]bool{}
	for _, a := range tree.Applications(t) {
		namespaces[a.Name] = true
	}
	var found []applicationPolicy
	for _, rel := range tracked(t, func(rel string) bool {
		return strings.HasSuffix(rel, ".yaml") || strings.HasSuffix(rel, ".yml")
	}) {
		// A file that is not YAML this reads holds no policy: Helm values
		// and templates are in the same tree.
		docs, isYAML := tree.MaybeYAML(t, rel)
		if !isYAML {
			continue
		}
		for _, d := range docs {
			var policy struct {
				Kind     string `yaml:"kind"`
				Metadata struct {
					Namespace string `yaml:"namespace"`
				} `yaml:"metadata"`
				Spec struct {
					Egress []map[string]any `yaml:"egress"`
				} `yaml:"spec"`
			}
			if as(d, &policy) != nil || policy.Kind != "NetworkPolicy" {
				continue
			}
			if !strings.HasPrefix(rel, applications.Dir+"/") && !namespaces[policy.Metadata.Namespace] {
				continue
			}
			found = append(found, policyOf(rel, policy.Spec.Egress))
		}
	}
	return found
}
