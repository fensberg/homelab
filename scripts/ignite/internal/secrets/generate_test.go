package secrets

import (
	"net/url"
	"strings"
	"testing"
)

// The generated password is spliced into a URI:
//
//	postgres://tofu:<pw>@10.10.10.100:30432/tofu_state?sslmode=require
//
// and buildStateConnStr parses a host and port back out of it with a regex.
// A password containing @ : / ? or # silently produces a different URI than
// intended - one that may still parse, and point somewhere else. That is the
// whole reason this generator exists rather than a call to a general-purpose
// password library.

func TestPassword_IsConnectionStringSafe(t *testing.T) {
	for i := 0; i < 200; i++ {
		pw, err := Password(32)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if bad := strings.ContainsAny(pw, "@:/?#[]%&= \t\n\"'\\"); bad {
			t.Fatalf("password %q contains a character that changes the meaning of a URI", pw)
		}
		// Prove it, rather than trusting the alphabet: the parsed host must
		// still be the host.
		u, err := url.Parse("postgres://tofu:" + pw + "@10.10.10.100:30432/tofu_state?sslmode=require")
		if err != nil {
			t.Fatalf("password %q produced an unparsable URI: %v", pw, err)
		}
		if u.Hostname() != "10.10.10.100" || u.Port() != "30432" {
			t.Fatalf("password %q moved the host to %s:%s", pw, u.Hostname(), u.Port())
		}
		if got, _ := u.User.Password(); got != pw {
			t.Fatalf("password did not survive a URI round trip: %q -> %q", pw, got)
		}
	}
}

func TestPassword_Length(t *testing.T) {
	for _, n := range []int{16, 32, 64} {
		pw, err := Password(n)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(pw) != n {
			t.Errorf("Password(%d) returned %d characters", n, len(pw))
		}
	}
}

// A generator that can return a short password is a generator that will,
// eventually, on the run nobody is watching.
func TestPassword_RefusesWeakLengths(t *testing.T) {
	for _, n := range []int{0, 1, 15, -1} {
		if _, err := Password(n); err == nil {
			t.Errorf("Password(%d) was accepted; anything under 16 characters must be refused", n)
		}
	}
}

func TestPassword_IsNotPredictable(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		pw, err := Password(32)
		if err != nil {
			t.Fatal(err)
		}
		if seen[pw] {
			t.Fatal("Password returned a duplicate within 500 draws - it is not using a CSPRNG")
		}
		seen[pw] = true
	}
}

// Every character in the alphabet must actually be reachable, or the entropy
// is lower than the length implies.
func TestPassword_UsesTheWholeAlphabet(t *testing.T) {
	seen := map[rune]bool{}
	for i := 0; i < 2000; i++ {
		pw, _ := Password(32)
		for _, r := range pw {
			seen[r] = true
		}
	}
	for _, r := range alphabet {
		if !seen[r] {
			t.Errorf("character %q was never generated in 64000 draws", r)
		}
	}
}
