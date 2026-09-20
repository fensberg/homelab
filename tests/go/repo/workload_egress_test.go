package repo

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The game server's egress policy may open private ranges, and not the one the
// estate lives in.
//
// WHY THIS IS A GUARD RATHER THAN THE PARAGRAPH IN THE MANIFEST. Which private
// ranges this policy permits is a PRODUCT decision: PlayFab Party offers a
// player's local address as a direct-path candidate, so the ranges open here
// are the networks a player may sit on. A player the policy denies joins,
// spawns, and is disconnected seconds later with nothing naming the policy -
// indistinguishable from their own connection being bad (#392).
//
// That makes "open another range" the obvious response to the next player who
// cannot connect, and 10.0.0.0/8 the obvious range, because it is the one
// remaining and it looks symmetrical with the two beside it. It is also the
// range this entire estate is addressed out of: node subnets at
// 10.<octet>.0.0/16, services at 10.96.0.0/12, pods at 10.244.0.0/16. Opening
// it hands a public-facing game server a UDP path to the API server, the state
// database, the hypervisor and every other workload - which is precisely what
// the policy exists to prevent.
//
// Nothing else would object. The manifest stays valid, kustomize builds it,
// Flux applies it, and the cluster does exactly what it was told. So the
// refusal lives here, and tests/mutations.yml proves it fires.

var ipBlockCIDR = regexp.MustCompile(`cidr:\s*(\S+)`)

// The estate's own space, and the reason it can never be a permitted peer.
const estateSpace = "10."

func TestTheGameServerCannotBeGivenAPathIntoTheEstate(t *testing.T) {
	// EVERY policy that selects this workload, not one file.
	//
	// NetworkPolicies are additive: a second one, anywhere in the repository,
	// selecting the same pods and permitting everything, opens the estate
	// without touching the file this guard used to read. That is not
	// hypothetical - it is exactly the shape a deliberate diagnostic took, and
	// the guard stayed green through it (#448).
	policies := policiesSelectingTheGameServer(t)
	if len(policies) == 0 {
		t.Fatal("no NetworkPolicy selecting the game server was found anywhere in the " +
			"repository, so this test is reading the wrong shape and proves nothing")
	}

	var permitted, excepted []string
	for _, p := range policies {
		gr, ex := permittedPeers(p.body)
		permitted = append(permitted, gr...)
		excepted = append(excepted, ex...)
		if p.openEgress {
			t.Errorf(`%s permits the game server egress to everything.

An empty egress rule is every address, which includes 10.0.0.0/8 - the space
this whole estate is addressed out of. A public-facing game server with a path
to the API server, the state database and the hypervisor is the single thing
this policy exists to prevent, and a second policy saying so quietly is worse
than the first one saying it out loud.`, p.file)
		}
	}

	if len(permitted) == 0 && len(excepted) == 0 {
		t.Fatal("no permitted destination was found in any policy selecting the game server, " +
			"so this test is reading the wrong shape and proves nothing")
	}

	for _, cidr := range permitted {
		if strings.HasPrefix(cidr, estateSpace) {
			t.Errorf(`the egress policy permits %s.

That is inside 10.0.0.0/8, which is the space this entire estate is addressed
out of - node subnets at 10.<octet>.0.0/16, services at 10.96.0.0/12, pods at
10.244.0.0/16.

This is a public-facing game server. Permitting it a path into that range gives
a compromised one reach to the API server, the state database, the hypervisor
and every other workload, which is the single thing this policy exists to
prevent.

If a player on a 10.x network cannot get a direct path, that is the correct
outcome and they stay on the relay. The isolation boundary wins.`, cidr)
		}
	}

	// And the broad internet rule must keep subtracting it, or the grant
	// arrives from the other direction with nothing above to notice.
	if !contains(excepted, "10.0.0.0/8") {
		t.Errorf(`the broad egress rule no longer excludes 10.0.0.0/8.

It permits 0.0.0.0/0 minus the private ranges. Dropping that exclusion opens
every address in the estate without any rule appearing to grant anything - the
change is a deleted line, and the diff is three characters shorter.

Excluded today: %s`, strings.Join(excepted, ", "))
	}
}

// The ranges players actually sit on stay open.
//
// The converse of the test above, and it needs saying: a guard that only ever
// refuses would be satisfied by closing everything, which breaks the workload
// for every player in the house and every player on a hotspot. #392 is a story
// about a range being closed, not about one being open.
func TestTheGameServerCanStillReachPlayersOnOrdinaryNetworks(t *testing.T) {
	var permitted []string
	for _, p := range policiesSelectingTheGameServer(t) {
		gr, _ := permittedPeers(p.body)
		permitted = append(permitted, gr...)
	}

	for _, want := range []struct{ cidr, who string }{
		{"192.168.0.0/16", "a player in the same house as the hypervisor"},
		{"172.16.0.0/12", "a player on a phone hotspot, in a hotel, or on a corporate network"},
	} {
		if !contains(permitted, want.cidr) {
			t.Errorf(`the egress policy no longer permits %s, so %s cannot be given a
direct path.

PlayFab Party offers the player's local address as a candidate, so a range that
is closed here is a player who joins, spawns, and is disconnected seconds later
with nothing anywhere naming this policy. That is #392, and it cost one remote
player every attempt they made.`, want.cidr, want.who)
		}
	}
}

// permittedPeers splits the policy's CIDRs into what it grants and what it
// subtracts from the broad rule.
//
// Split by indentation rather than by parsing, because an `except:` entry and a
// permitted `cidr:` are the same text at different depths, and counting them
// together would make the broad internet rule look like a grant of every
// private range - which is the exact opposite of what it is.
func permittedPeers(body string) (permitted, excepted []string) {
	inExcept := false
	exceptIndent := 0

	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))

		if inExcept && indent <= exceptIndent && !strings.HasPrefix(trimmed, "- ") {
			inExcept = false
		}
		if strings.HasPrefix(trimmed, "except:") {
			inExcept, exceptIndent = true, indent
			continue
		}
		if inExcept {
			if v := strings.TrimPrefix(trimmed, "- "); v != trimmed {
				excepted = append(excepted, v)
			}
			continue
		}
		if m := ipBlockCIDR.FindStringSubmatch(trimmed); m != nil && m[1] != "0.0.0.0/0" {
			permitted = append(permitted, m[1])
		}
	}
	return permitted, excepted
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// A NetworkPolicy somewhere in the repository that governs the game server's
// pods, with the text of its document and whether its egress is unrestricted.
type gameServerPolicy struct {
	file       string
	body       string
	openEgress bool
}

// policiesSelectingTheGameServer walks the whole repository rather than naming
// a file.
//
// Policies are additive and a namespace can hold as many as anybody writes, so
// the question "what may this workload reach" is answered by their union. A
// guard that reads one path answers a narrower question that looks identical
// until somebody adds a second file.
func policiesSelectingTheGameServer(t *testing.T) []gameServerPolicy {
	t.Helper()
	root := repoRoot(t)
	var found []gameServerPolicy

	for _, dir := range []string{"clusters", "environments", "modules"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !(strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml")) {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			for _, doc := range strings.Split(string(data), "\n---") {
				var parsed struct {
					Kind     string `yaml:"kind"`
					Metadata struct {
						Namespace string `yaml:"namespace"`
					} `yaml:"metadata"`
					Spec struct {
						PodSelector struct {
							MatchLabels map[string]string `yaml:"matchLabels"`
						} `yaml:"podSelector"`
						Egress []map[string]any `yaml:"egress"`
					} `yaml:"spec"`
				}
				if yaml.Unmarshal([]byte(doc), &parsed) != nil || parsed.Kind != "NetworkPolicy" {
					continue
				}
				// Either it names the workload, or it selects everything in
				// the workload's namespace - both govern these pods.
				name, named := parsed.Spec.PodSelector.MatchLabels["app.kubernetes.io/name"]
				selectsAll := len(parsed.Spec.PodSelector.MatchLabels) == 0 && parsed.Metadata.Namespace == "valheim"
				if !(named && name == "valheim") && !selectsAll {
					continue
				}
				open := false
				for _, rule := range parsed.Spec.Egress {
					if len(rule) == 0 {
						open = true
					}
				}
				found = append(found, gameServerPolicy{file: rel, body: doc, openEgress: open})
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}
	return found
}
