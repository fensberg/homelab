package secrets

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

func parsed(t *testing.T, certificate string) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode([]byte(certificate))
	if block == nil {
		t.Fatal("not PEM")
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func verifies(t *testing.T, c ScrapeCredentials, certificate string, usage x509.ExtKeyUsage, name string, at time.Time) error {
	t.Helper()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(c.Authority)) {
		t.Fatal("the authority is not a certificate")
	}
	_, err := parsed(t, certificate).Verify(x509.VerifyOptions{
		Roots: roots, DNSName: name, KeyUsages: []x509.ExtKeyUsage{usage}, CurrentTime: at,
	})
	return err
}

var aDay = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

func TestTheExportersCertificateIsGoodForItsNameAndNoOther(t *testing.T) {
	c, err := ScrapeTLS("exporter.example.internal", aDay)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifies(t, c, c.Certificate, x509.ExtKeyUsageServerAuth, "exporter.example.internal", aDay); err != nil {
		t.Errorf("the exporter's certificate does not verify for its own name: %v", err)
	}
	if err := verifies(t, c, c.Certificate, x509.ExtKeyUsageServerAuth, "something.else", aDay); err == nil {
		t.Error("the exporter's certificate verifies for a name it was not given")
	}
}

// Each certificate does one job. The scraper's presented as a server's, or
// the exporter's presented by a client, is refused.
func TestEachCertificateIsGoodForItsOwnSideOnly(t *testing.T) {
	c, err := ScrapeTLS("exporter.example.internal", aDay)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifies(t, c, c.ScraperCertificate, x509.ExtKeyUsageClientAuth, "", aDay); err != nil {
		t.Errorf("the scraper's certificate is not good for a client: %v", err)
	}
	if err := verifies(t, c, c.ScraperCertificate, x509.ExtKeyUsageServerAuth, "", aDay); err == nil {
		t.Error("the scraper's certificate would be accepted as a server's")
	}
	if err := verifies(t, c, c.Certificate, x509.ExtKeyUsageClientAuth, "", aDay); err == nil {
		t.Error("the exporter's certificate would be accepted from a client")
	}
}

func TestEachKeyIsItsCertificatesAndNeitherCanSign(t *testing.T) {
	c, err := ScrapeTLS("exporter.example.internal", aDay)
	if err != nil {
		t.Fatal(err)
	}
	for name, pair := range map[string][2]string{
		"the exporter's": {c.Certificate, c.PrivateKey},
		"the scraper's":  {c.ScraperCertificate, c.ScraperPrivateKey},
	} {
		if _, err := tls.X509KeyPair([]byte(pair[0]), []byte(pair[1])); err != nil {
			t.Errorf("%s key is not its certificate's: %v", name, err)
		}
		if parsed(t, pair[0]).IsCA {
			t.Errorf("%s certificate can sign others", name)
		}
	}
	if _, err := tls.X509KeyPair([]byte(c.Authority), []byte(c.PrivateKey)); err == nil {
		t.Error("the exporter's key is the authority's: whoever holds it can mint a certificate")
	}
}

// A second set is a different authority: a certificate from one estate, or
// from before a replacement, is nothing to this one.
func TestACertificateFromAnotherAuthorityIsRefused(t *testing.T) {
	mine, err := ScrapeTLS("exporter.example.internal", aDay)
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := ScrapeTLS("exporter.example.internal", aDay)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifies(t, mine, theirs.ScraperCertificate, x509.ExtKeyUsageClientAuth, "", aDay); err == nil {
		t.Error("a scraper signed by another authority is admitted")
	}
}

func TestTheCertificatesLastYearsAndThenStop(t *testing.T) {
	c, err := ScrapeTLS("exporter.example.internal", aDay)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifies(t, c, c.Certificate, x509.ExtKeyUsageServerAuth, "", aDay.AddDate(9, 0, 0)); err != nil {
		t.Errorf("not good nine years on: %v", err)
	}
	if err := verifies(t, c, c.Certificate, x509.ExtKeyUsageServerAuth, "", aDay.AddDate(11, 0, 0)); err == nil {
		t.Error("still good eleven years on")
	}
}

func TestAnExporterWithNoNameIsRefused(t *testing.T) {
	if _, err := ScrapeTLS("", aDay); err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("got %v", err)
	}
}
