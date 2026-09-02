package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	// 2048 is what GitHub issues. Smaller would be faster here, but a test
	// that signs a different size than production is testing something else.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a test key: %v", err)
	}
	return key
}

// The JWT is what proves we hold the App's private key. Its three parts are
// base64url with no padding - standard base64 would be rejected by GitHub, and
// the failure looks like an authentication problem rather than an encoding one.
func TestAppJWTIsThreeBase64URLParts(t *testing.T) {
	tok, err := appJWT(testKey(t), "4753166", time.Now())
	if err != nil {
		t.Fatalf("appJWT: %v", err)
	}

	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("got %d parts, want 3", len(parts))
	}
	for i, p := range parts {
		if strings.ContainsAny(p, "+/=") {
			t.Errorf("part %d contains standard-base64 characters (+ / =): %q", i, p)
		}
		if _, err := base64.RawURLEncoding.DecodeString(p); err != nil {
			t.Errorf("part %d is not valid base64url: %v", i, err)
		}
	}
}

func TestAppJWTClaims(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tok, err := appJWT(testKey(t), "4753166", now)
	if err != nil {
		t.Fatalf("appJWT: %v", err)
	}

	raw, err := base64.RawURLEncoding.DecodeString(strings.Split(tok, ".")[1])
	if err != nil {
		t.Fatalf("decoding claims: %v", err)
	}
	var claims struct {
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
		Iss string `json:"iss"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("parsing claims: %v", err)
	}

	if claims.Iss != "4753166" {
		t.Errorf("iss = %q, want the app id", claims.Iss)
	}
	// Backdated deliberately: GitHub rejects a JWT whose iat is in the future
	// by even a second, and the workstation's clock is not guaranteed to agree
	// with theirs.
	if claims.Iat >= now.Unix() {
		t.Errorf("iat = %d, want it backdated below %d for clock skew", claims.Iat, now.Unix())
	}
	// GitHub's hard ceiling is 10 minutes; anything at or above it is rejected
	// outright, so the margin is not cosmetic.
	if ttl := claims.Exp - now.Unix(); ttl <= 0 || ttl >= 600 {
		t.Errorf("exp is %ds away, want 0 < ttl < 600", ttl)
	}
}

// A JWT that cannot be verified with the public half is a JWT GitHub will
// reject, and the error it returns says only "bad credentials".
func TestAppJWTVerifiesAgainstThePublicKey(t *testing.T) {
	key := testKey(t)
	tok, err := appJWT(key, "4753166", time.Now())
	if err != nil {
		t.Fatalf("appJWT: %v", err)
	}
	parts := strings.Split(tok, ".")
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decoding signature: %v", err)
	}
	if err := verifyRS256(&key.PublicKey, parts[0]+"."+parts[1], sig); err != nil {
		t.Errorf("signature does not verify against its own public key: %v", err)
	}
}

// verifyRS256 proves a produced JWT verifies against its own public half.
//
// It lives here rather than beside signRS256 because it exists only for this
// test - and a helper that production code does not call is a helper that
// makes the production file look like it has coverage it does not.
// tests/go/repo/change_detector_test.go is what noticed.
//
// The check earns its place: GitHub's rejection message for a bad signature is
// indistinguishable from one for a wrong app id, so a signing bug found here
// is a signing bug that would otherwise be diagnosed as a configuration one.
func verifyRS256(pub *rsa.PublicKey, signingInput string, sig []byte) error {
	digest := sha256.Sum256([]byte(signingInput))
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig)
}
