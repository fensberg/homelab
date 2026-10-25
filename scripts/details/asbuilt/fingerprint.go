package asbuilt

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"homelab/details/secrets"
	"regexp"
	"strings"
)

// Fingerprinter replaces a value with a stand-in derived from it under a key.
//
// Keyed rather than a bare hash because most of what it replaces is low
// entropy. An address or a hostname hashed without a key can be recovered by
// hashing every candidate; under a key nobody holds, it cannot.
type Fingerprinter struct{ key []byte }

// NewFingerprinter refuses a short key, because a guessable key turns every
// fingerprint back into a hash anybody can test candidates against.
func NewFingerprinter(key []byte) (*Fingerprinter, error) {
	if len(key) < 32 {
		return nil, fmt.Errorf("a fingerprint key needs at least 32 bytes, got %d", len(key))
	}
	return &Fingerprinter{key: key}, nil
}

func (f *Fingerprinter) mac(v string) []byte {
	m := hmac.New(sha256.New, f.key)
	m.Write([]byte(v))
	return m.Sum(nil)
}

// Opaque is the default stand-in: lower-case, alphanumeric and starting with
// a letter, so it is valid wherever a DNS label, a bucket name or a Kubernetes
// name is expected.
func (f *Fingerprinter) Opaque(v string) string {
	return "r" + hex.EncodeToString(f.mac(v))[:12]
}

// ipv4 is an address the code uses as an address.
var ipv4 = regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}$`)

// shapes are the fields whose value the code takes apart or validates, so a
// stand-in has to keep the shape. Anything not listed is opaque, and a field
// that needed a shape and lacks one fails the offline plan loudly - which is
// how this table grows, rather than by anybody predicting it.
var shapes = map[string]func(f *Fingerprinter, v string) string{
	// An address is recognised by its value, but a field a change adds has
	// no value yet, only a reference; its name is what says it is one.
	"ip": (*Fingerprinter).address,
	// Split on "/" to find the owner and repository.
	"repo_url":    (*Fingerprinter).url,
	"webhook_url": (*Fingerprinter).url,
	// The estate's repository, split the same way.
	"url": (*Fingerprinter).url,
	// Cloudflare checks the token's format when the provider configures:
	// forty characters.
	"api_token": func(f *Fingerprinter, v string) string { return f.hex(v, 40) },
	// The App that delivers unreviewed, whose id the OpenTofu takes as a
	// number.
	"procurement_app_id": (*Fingerprinter).digits,
	// Proxmox checks the token's format when the provider configures.
	"token_id":     (*Fingerprinter).proxmoxTokenID,
	"token_secret": (*Fingerprinter).uuid,
	// binary_data takes an already-encoded value.
	"private_key": (*Fingerprinter).base64,
	// The host's scrape credentials, each a PEM block kept the same way.
	"authority":                     (*Fingerprinter).base64,
	secrets.ScraperCertificateField: (*Fingerprinter).base64,
	secrets.ScraperPrivateKeyField:  (*Fingerprinter).base64,
	// Part of an endpoint's hostname, and hex in every real account.
	"account_id": func(f *Fingerprinter, v string) string { return f.hex(v, 32) },
}

// Field fingerprints a value of the named config field.
func (f *Fingerprinter) Field(field, v string) string {
	if ipv4.MatchString(v) {
		return f.address(v)
	}
	if shape, ok := shapes[field]; ok {
		return shape(f, v)
	}
	return f.Opaque(v)
}

// address is a stand-in in 198.18.0.0/15, which is reserved for
// benchmarking, so it can never be mistaken for, or collide with, a real
// address.
func (f *Fingerprinter) address(v string) string {
	d := f.mac(v)
	return fmt.Sprintf("198.%d.%d.%d", 18+int(d[0]&1), d[1], d[2])
}

func (f *Fingerprinter) url(v string) string {
	if strings.HasPrefix(strings.TrimSpace(v), "{{") {
		// A field with no value yet, only the vault's reference to one, as a
		// plan from nothing has it: a URL of the shape every one of these
		// has, an owner and a name below a host.
		return "https://" + f.Opaque(v) + ".invalid/" + f.Opaque(v+"/owner") + "/" + f.Opaque(v+"/name")
	}
	scheme, rest, found := strings.Cut(v, "://")
	if !found {
		return f.Opaque(v)
	}
	host, path, _ := strings.Cut(rest, "/")
	parts := []string{}
	for _, p := range strings.Split(path, "/") {
		parts = append(parts, f.Opaque(p))
	}
	return scheme + "://" + f.Opaque(host) + ".invalid/" + strings.Join(parts, "/")
}

// proxmoxTokenID keeps user@realm!name.
func (f *Fingerprinter) proxmoxTokenID(v string) string {
	user, rest, _ := strings.Cut(v, "@")
	realm, name, _ := strings.Cut(rest, "!")
	return f.Opaque(user) + "@" + f.Opaque(realm) + "!" + f.Opaque(name)
}

func (f *Fingerprinter) uuid(v string) string {
	h := f.hex(v, 32)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func (f *Fingerprinter) hex(v string, n int) string {
	return strings.Repeat(hex.EncodeToString(f.mac(v)), 2)[:n]
}

// digits is a stand-in for a number: eight digits, the first never a zero.
func (f *Fingerprinter) digits(v string) string {
	d := f.mac(v)
	out := []byte{'1' + d[0]%9}
	for _, b := range d[1:8] {
		out = append(out, '0'+b%10)
	}
	return string(out)
}

func (f *Fingerprinter) base64(v string) string {
	return base64.StdEncoding.EncodeToString(f.mac(v))
}

// dnsLabel is the sanitising the cluster root applies to a site's name before
// using it as a VM or cluster name. A value stored in that form is not a
// substring of the value as the vault holds it, so it is replaced as a
// variant in its own right.
func dnsLabel(v string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(v) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
