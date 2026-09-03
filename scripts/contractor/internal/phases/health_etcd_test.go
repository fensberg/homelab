package phases

import (
	"strings"
	"testing"
)

// The output shape talosctl actually prints, taken from the scale-up that
// motivated #137 - five members, all voting, which is what a healthy 3-of-5
// quorum looks like.
const fiveHealthyMembers = `NODE           ID                   HOSTNAME       PEER URLS                   CLIENT URLS                 LEARNER
192.0.2.100    aaaaaaaaaaaaaaaa     site0-cp-100   https://192.0.2.100:2380    https://192.0.2.100:2379    false
192.0.2.101    bbbbbbbbbbbbbbbb     site0-cp-101   https://192.0.2.101:2380    https://192.0.2.101:2379    false
192.0.2.102    cccccccccccccccc     site0-cp-102   https://192.0.2.102:2380    https://192.0.2.102:2379    false
192.0.2.103    dddddddddddddddd     site0-cp-103   https://192.0.2.103:2380    https://192.0.2.103:2379    false
192.0.2.104    eeeeeeeeeeeeeeee     site0-cp-104   https://192.0.2.104:2380    https://192.0.2.104:2379    false`

func TestParseEtcdMembersReadsEveryRow(t *testing.T) {
	members, err := parseEtcdMembers(fiveHealthyMembers)
	if err != nil {
		t.Fatalf("parsing the real output shape failed: %v", err)
	}
	if len(members) != 5 {
		t.Fatalf("got %d members, want 5", len(members))
	}
	if members[3].Hostname != "site0-cp-103" {
		t.Errorf("hostname = %q, want site0-cp-103", members[3].Hostname)
	}
	for _, m := range members {
		if m.Learner {
			t.Errorf("%s parsed as a learner; every LEARNER field here is false", m.Hostname)
		}
	}
}

// The whole point of the check. A member that joined and does not vote is
// invisible to every other signal in the Health phase.
func TestJudgeEtcdRefusesALearner(t *testing.T) {
	out := strings.Replace(fiveHealthyMembers,
		"https://192.0.2.104:2379    false",
		"https://192.0.2.104:2379    true", 1)
	members, err := parseEtcdMembers(out)
	if err != nil {
		t.Fatal(err)
	}
	err = judgeEtcd(members, 5)
	if err == nil {
		t.Fatal("a learner passed. Five members with one not voting is a quorum of " +
			"three against four votes - less fault-tolerant than the three-node cluster")
	}
	if !strings.Contains(err.Error(), "site0-cp-104") {
		t.Errorf("got %v, want the learner named", err)
	}
}

// The other half: a machine that exists, is Ready, and never joined.
func TestJudgeEtcdRefusesAShortMemberList(t *testing.T) {
	members, err := parseEtcdMembers(fiveHealthyMembers)
	if err != nil {
		t.Fatal(err)
	}
	if err := judgeEtcd(members[:3], 5); err == nil {
		t.Fatal("three members passed a five-node config. That is the scale-up " +
			"failure this check exists for: quorum moved, the votes did not")
	}
}

func TestJudgeEtcdAcceptsAFullVotingMembership(t *testing.T) {
	members, err := parseEtcdMembers(fiveHealthyMembers)
	if err != nil {
		t.Fatal(err)
	}
	if err := judgeEtcd(members, 5); err != nil {
		t.Errorf("a healthy five-member cluster was refused: %v", err)
	}
}

// Not knowing and being told everything is fine must not look the same.
//
// If talosctl ever drops or renames the column, a parser that shrugged would
// report every member as a voter - the check would go green precisely because
// it had stopped being able to see.
func TestParseEtcdMembersRefusesOutputWithNoLearnerColumn(t *testing.T) {
	const out = `NODE           ID                 HOSTNAME
192.0.2.100    aaaaaaaaaaaaaaaa   site0-cp-100`
	if _, err := parseEtcdMembers(out); err == nil {
		t.Fatal("output with no LEARNER column parsed cleanly, so every member would " +
			"have been reported as voting")
	}
}

// An empty answer is not a healthy one. A cluster that has not come up yet and
// a cluster with no members print nearly the same thing.
func TestParseEtcdMembersRefusesAnEmptyMemberList(t *testing.T) {
	for _, out := range []string{
		"",
		"NODE           ID                 HOSTNAME       LEARNER",
	} {
		if _, err := parseEtcdMembers(out); err == nil {
			t.Errorf("%q parsed as a healthy cluster", out)
		}
	}
}
