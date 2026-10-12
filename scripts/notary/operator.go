package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/acme"

	"homelab/details/console"
	"homelab/details/onepassword"
)

// The issuer, and what is asked of it. Variables where a test asks a
// stand-in.
const (
	// issuerName is what a domain's records call the issuer.
	issuerName = "letsencrypt.org"
	// profile is the issuer's own name for the kind of certificate asked
	// for. It is the issuer's to rename, so what comes back is measured
	// against longestLife and not trusted to be what the name once meant.
	profile = "tlsserver"
	// longestLife is the longest a certificate may be good for and still be
	// granted: forty-five days and the hour the issuer backdates one by.
	longestLife = 46 * 24 * time.Hour
)

var (
	directory = "https://acme-v02.api.letsencrypt.org/directory"
	rehearsal = "https://acme-staging-v02.api.letsencrypt.org/directory"
	// settle is how long a record is given to be seen from where the issuer
	// looks, before the issuer is told to look.
	settle = 30 * time.Second
)

// issuerAccount is the operator as the issuer knows them.
type issuerAccount struct {
	client *acme.Client
	uri    string
}

// issued is one certificate as a vault keeps it.
type issued struct {
	chain, key string
	expires    time.Time
}

// operator registers the account key with the issuer, or finds the account
// it already is. A rehearsal uses a key made for it and thrown away: the
// rehearsal service is another issuer, and the operator's key is not shown
// to anything it does not have to be.
func operator(ctx context.Context, rehearse bool) (issuerAccount, error) {
	var key *ecdsa.PrivateKey
	var err error
	at := directory
	if rehearse {
		at = rehearsal
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	} else {
		var kept, status string
		kept, status, err = onepassword.EnsureField(accountKey, func() (string, error) {
			_, encoded, err := newKey()
			return encoded, err
		})
		if err != nil {
			return issuerAccount{}, err
		}
		if status == "generated" {
			console.Ok("generated the key the issuer knows the operator by and stored it in the estate vault")
		}
		key, err = keyFrom(kept)
	}
	if err != nil {
		return issuerAccount{}, err
	}
	client := &acme.Client{Key: key, DirectoryURL: at}
	account, err := client.Register(ctx, &acme.Account{}, acme.AcceptTOS)
	if errors.Is(err, acme.ErrAccountAlreadyExists) {
		account, err = client.GetReg(ctx, "")
	}
	if err != nil {
		return issuerAccount{}, fmt.Errorf("registering with the issuer: %w", err)
	}
	return issuerAccount{client, account.URI}, nil
}

// certificate asks the issuer for one certificate for exactly these names,
// proving each by a record in the zone, and returns it with a key made for
// it. Every record made is removed, however this ends.
func (a issuerAccount) certificate(ctx context.Context, z zone, names []string) (got issued, err error) {
	order, err := a.client.AuthorizeOrder(ctx, acme.DomainIDs(names...), acme.WithOrderIssuanceProfile(profile))
	if err != nil {
		return got, fmt.Errorf("ordering: %w", err)
	}
	var remove []func() error
	defer func() {
		for _, each := range remove {
			if gone := each(); gone != nil && err == nil {
				err = fmt.Errorf("the certificate was issued and a record that proved a name could not be removed: %w", gone)
			}
		}
	}()
	var asked []*acme.Challenge
	for _, at := range order.AuthzURLs {
		authz, err := a.client.GetAuthorization(ctx, at)
		if err != nil {
			return got, err
		}
		if authz.Status == acme.StatusValid {
			continue
		}
		var byRecord *acme.Challenge
		for _, c := range authz.Challenges {
			if c.Type == "dns-01" {
				byRecord = c
			}
		}
		if byRecord == nil {
			return got, errors.New("the issuer does not offer to take a record as proof of a name, and a record is the only proof the notary can give")
		}
		value, err := a.client.DNS01ChallengeRecord(byRecord.Token)
		if err != nil {
			return got, err
		}
		gone, err := z.prove(authz.Identifier.Value, value)
		if err != nil {
			return got, err
		}
		remove = append(remove, gone)
		asked = append(asked, byRecord)
	}
	if len(asked) > 0 {
		select {
		case <-ctx.Done():
			return got, ctx.Err()
		case <-time.After(settle):
		}
	}
	for _, c := range asked {
		if _, err := a.client.Accept(ctx, c); err != nil {
			return got, fmt.Errorf("telling the issuer to look: %w", err)
		}
	}
	for _, at := range order.AuthzURLs {
		if _, err := a.client.WaitAuthorization(ctx, at); err != nil {
			return got, fmt.Errorf("the issuer did not accept the proof of a name: %w", err)
		}
	}
	if order, err = a.client.WaitOrder(ctx, order.URI); err != nil {
		return got, fmt.Errorf("the order did not become ready: %w", err)
	}

	key, encoded, err := newKey()
	if err != nil {
		return got, err
	}
	request, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{DNSNames: names}, key)
	if err != nil {
		return got, err
	}
	chain, _, err := a.client.CreateOrderCert(ctx, order.FinalizeURL, request, true)
	if err != nil {
		return got, fmt.Errorf("the issuer did not issue: %w", err)
	}
	if len(chain) == 0 {
		return got, errors.New("the issuer answered with no certificate")
	}
	leaf, err := x509.ParseCertificate(chain[0])
	if err != nil {
		return got, err
	}
	if life := leaf.NotAfter.Sub(leaf.NotBefore); life > longestLife {
		return got, fmt.Errorf("the issuer gave a certificate good for %d days where at most %d is granted. What it calls %q has changed, and a site is not handed a longer subscription than the operator chose",
			int(life.Hours()/24), int(longestLife.Hours()/24), profile)
	}
	var whole []byte
	for _, der := range chain {
		whole = append(whole, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	return issued{base64.StdEncoding.EncodeToString(whole), encoded, leaf.NotAfter}, nil
}

// newKey makes a key and the form a vault keeps it in: PEM, base64-encoded
// onto one line.
func newKey() (*ecdsa.PrivateKey, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, "", err
	}
	return key, base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), nil
}

func keyFrom(kept string) (*ecdsa.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(kept)
	if err != nil {
		return nil, errors.New("the key the issuer knows the operator by is not base64 in the vault")
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("the key the issuer knows the operator by is not a key in the vault")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("the key the issuer knows the operator by is not of the kind the notary makes")
	}
	return key, nil
}
