package ghapi

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AppCredential is what the vault holds of one installation of an App, as
// every config here carries it: the same three fields under the same three
// keys, whichever program reads them.
type AppCredential struct {
	AppID          string `json:"app_id"`
	InstallationID string `json:"installation_id"`

	// Base64 of the PEM, not the PEM. `op inject` substitutes values into a
	// JSON template, and a PEM's newlines would produce a file that does not
	// parse - so the encoding is a property of how the value travels, not a
	// security measure.
	PrivateKey string `json:"private_key"`
}

// App is the installation this credential signs in as, at base. A key that
// is not base64 is refused here, and its contents are not quoted.
func (c AppCredential) App(base string) (App, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(c.PrivateKey))
	if err != nil {
		return App{}, errors.New("the App's private key is not base64: it is kept as the .pem file encoded onto one line")
	}
	return App{Base: base, ID: c.AppID, InstallationID: c.InstallationID, Key: key}, nil
}

// App is a GitHub App as one installation of it: what it takes to act as
// that installation and nothing else.
type App struct {
	// Base is the API to speak to; empty is GitHub's own.
	Base           string
	ID             string
	InstallationID string
	// Key is the App's private key, PEM-encoded.
	Key []byte
}

// Token signs in as the installation and returns the token it is handed,
// which lasts an hour and can do what the installation was granted.
func (a App) Token(now time.Time) (string, error) {
	assertion, err := a.assertion(now)
	if err != nil {
		return "", err
	}
	var answer struct {
		Token string `json:"token"`
	}
	path := fmt.Sprintf("/app/installations/%s/access_tokens", a.InstallationID)
	if err := call(http.MethodPost, URL(a.Base, "%s", path), assertion, &answer); err != nil {
		return "", err
	}
	if answer.Token == "" {
		return "", errors.New("GitHub answered the sign-in without a token")
	}
	return answer.Token, nil
}

// assertion proves the private key is held. Base64url without padding, and
// issued a minute ago: GitHub refuses one issued a second in its own future.
func (a App) assertion(now time.Time) (string, error) {
	block, _ := pem.Decode(a.Key)
	if block == nil {
		return "", errors.New("the App's private key is not PEM-encoded")
	}
	var key *rsa.PrivateKey
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		key = k
	} else if parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes); err != nil {
		return "", errors.New("the App's private key is not an RSA key in PKCS#1 or PKCS#8 form")
	} else if k, is := parsed.(*rsa.PrivateKey); !is {
		return "", fmt.Errorf("the App's private key holds a %T, not an RSA key", parsed)
	} else {
		key = k
	}

	part := func(v any) string {
		raw, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	signed := part(map[string]string{"alg": "RS256", "typ": "JWT"}) + "." + part(map[string]any{
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": a.ID,
	})
	digest := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("signing the App's assertion: %w", err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// Get asks base+path with a token and reads the answer into a T. A refusal
// is an error naming the status and GitHub's own reason, never an empty
// result: an empty list read out of a 403 is an answer somebody acts on.
func Get[T any](base, token, path string) (T, error) {
	var out T
	err := call(http.MethodGet, URL(base, "%s", path), token, &out)
	return out, err
}

func call(method, url, bearer string, into any) error {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("asking GitHub: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("reading GitHub's answer: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var said struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &said)
		if said.Message == "" {
			said.Message = "no reason given"
		}
		return fmt.Errorf("GitHub refused (%d): %s", resp.StatusCode, said.Message)
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(into); err != nil {
		return fmt.Errorf("GitHub answered %d with something that is not what was asked for: %w", resp.StatusCode, err)
	}
	return nil
}
