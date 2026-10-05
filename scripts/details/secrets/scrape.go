package secrets

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

// ScrapeCredentials is what an exporter and the one thing allowed to scrape
// it need to trust each other: an authority, and a certificate and key for
// each side, signed by it.
//
// The authority's own key is not here and is kept nowhere. It signs these two
// certificates and is dropped, so nothing that later reads the vault can mint
// a third. Replacing them is generating all of it again.
type ScrapeCredentials struct {
	// Authority is the certificate both sides verify the other against.
	Authority string
	// Certificate and PrivateKey are the exporter's, valid for its name.
	Certificate string
	PrivateKey  string
	// ScraperCertificate and ScraperPrivateKey are what the scraper presents.
	ScraperCertificate string
	ScraperPrivateKey  string
}

// What the scraper's half is called wherever it is kept: in the vault, in the
// config, and in the stand-in a plan against the as-built record makes for
// it. One spelling, because each of those reads the other's.
const (
	ScraperCertificateField = "scraper_certificate"
	ScraperPrivateKeyField  = "scraper_private_key"
)

// scrapeLifetime is how long the certificates are good for. Long, because
// nothing renews them yet and an exporter that stops answering on a date is a
// worse fault than a certificate that is old; the scrape failing is what
// reports the day it comes.
const scrapeLifetime = 10 * 365 * 24 * time.Hour

// ScrapeTLS generates credentials for an exporter that answers as serverName
// and admits one scraper. Everything is PEM.
func ScrapeTLS(serverName string, now time.Time) (ScrapeCredentials, error) {
	if serverName == "" {
		return ScrapeCredentials{}, fmt.Errorf("an exporter's certificate has to name what it answers as")
	}
	authorityKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return ScrapeCredentials{}, fmt.Errorf("generating the authority's key: %w", err)
	}
	authority, err := certificateTemplate("scrape authority for "+serverName, now)
	if err != nil {
		return ScrapeCredentials{}, err
	}
	authority.IsCA = true
	authority.BasicConstraintsValid = true
	authority.MaxPathLenZero = true
	authority.KeyUsage = x509.KeyUsageCertSign
	authorityDER, err := x509.CreateCertificate(rand.Reader, authority, authority, &authorityKey.PublicKey, authorityKey)
	if err != nil {
		return ScrapeCredentials{}, fmt.Errorf("signing the authority: %w", err)
	}
	signed, err := x509.ParseCertificate(authorityDER)
	if err != nil {
		return ScrapeCredentials{}, err
	}

	leaf := func(name string, usage x509.ExtKeyUsage, dnsNames []string) (cert, key string, err error) {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return "", "", fmt.Errorf("generating a key for %s: %w", name, err)
		}
		tmpl, err := certificateTemplate(name, now)
		if err != nil {
			return "", "", err
		}
		tmpl.BasicConstraintsValid = true
		tmpl.KeyUsage = x509.KeyUsageDigitalSignature
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{usage}
		tmpl.DNSNames = dnsNames
		der, err := x509.CreateCertificate(rand.Reader, tmpl, signed, &k.PublicKey, authorityKey)
		if err != nil {
			return "", "", fmt.Errorf("signing a certificate for %s: %w", name, err)
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(k)
		if err != nil {
			return "", "", err
		}
		return pemOf("CERTIFICATE", der), pemOf("PRIVATE KEY", keyDER), nil
	}

	out := ScrapeCredentials{Authority: pemOf("CERTIFICATE", authorityDER)}
	if out.Certificate, out.PrivateKey, err = leaf(serverName, x509.ExtKeyUsageServerAuth, []string{serverName}); err != nil {
		return ScrapeCredentials{}, err
	}
	if out.ScraperCertificate, out.ScraperPrivateKey, err = leaf("scraper of "+serverName, x509.ExtKeyUsageClientAuth, nil); err != nil {
		return ScrapeCredentials{}, err
	}
	return out, nil
}

func certificateTemplate(commonName string, now time.Time) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, fmt.Errorf("reading from the system random source: %w", err)
	}
	return &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		// A minute back, so a clock a little behind this one does not read the
		// certificate as not yet valid.
		NotBefore: now.Add(-time.Minute),
		NotAfter:  now.Add(scrapeLifetime),
	}, nil
}

func pemOf(kind string, der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}))
}
