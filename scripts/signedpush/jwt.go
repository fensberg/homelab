package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"time"
)

// appJWT builds the assertion that proves we hold the App's private key.
//
// Two details are load-bearing and neither fails visibly when wrong:
// base64url without padding (standard base64 is rejected, and the error says
// only "bad credentials"), and an iat backdated for clock skew, because GitHub
// refuses a JWT issued even a second in its own future.
func appJWT(key *rsa.PrivateKey, appID string, now time.Time) (string, error) {
	b64 := func(v any) (string, error) {
		raw, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return base64.RawURLEncoding.EncodeToString(raw), nil
	}

	header, err := b64(map[string]string{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	// Nine minutes, inside GitHub's ten-minute ceiling with room for the skew
	// the backdated iat is already conceding.
	claims, err := b64(map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": appID,
	})
	if err != nil {
		return "", err
	}

	signingInput := header + "." + claims
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("signing the app assertion: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// loadKey reads the App private key. GitHub issues PKCS#1 ("BEGIN RSA PRIVATE
// KEY"); PKCS#8 is accepted too so that a key converted by openssl still works.
func loadKey(path string) (*rsa.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the app private key: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		// Deliberately does not quote the file's contents.
		return nil, fmt.Errorf("%s is not PEM-encoded", path)
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s is not an RSA private key in PKCS#1 or PKCS#8 form", path)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s holds a %T, not an RSA key", path, parsed)
	}
	return key, nil
}
