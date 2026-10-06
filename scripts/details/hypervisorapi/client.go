// Package hypervisorapi reaches a hypervisor's API as the one authority that
// can vouch for it says it is.
//
// A hypervisor signs its API certificate with an authority it made when it
// was installed, which no public list knows. For as long as nothing here held
// that authority, everything that talked to the API did so with certificate
// verification switched off - the contractor, two test tiers - and anything
// able to stand between them and the host could have answered as it, and
// been handed the API token.
//
// The hypervisor playbook reads the authority off the host and keeps it in
// the site's vault. This is the other half: a client that trusts that
// authority and no other, for the one node it was told to expect.
package hypervisorapi

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// AuthorityRef is where a site's vault keeps its hypervisor's authority: the
// certificate, base64-encoded on one line. One spelling, because the playbook
// writes it and everything that reads it has to look in the same place.
func AuthorityRef(site string) string {
	return fmt.Sprintf("op://%s/hypervisor/authority", site)
}

// StorageDriverTokenRefs is where a site's vault keeps the storage driver's
// token for the hypervisor's API: its id and its secret. The playbook writes
// them, and whatever reads them looks here.
func StorageDriverTokenRefs(site string) (id, secret string) {
	return fmt.Sprintf("op://%s/hypervisor/storage_driver_token_id", site),
		fmt.Sprintf("op://%s/hypervisor/storage_driver_token_secret", site)
}

// DriverVolume is the name the storage driver gives a volume it makes: owned
// by a machine id that is no machine's, so that no machine's destruction
// takes it.
func DriverVolume(name string) string { return "vm-9999-" + name }

// DriverVolumeOwner is that machine id.
const DriverVolumeOwner = "9999"

// Client is an HTTP client for one node of a hypervisor.
//
// It trusts the authority it is given and nothing else: not the system's
// roots, so a certificate from a public authority for some other name is as
// much a stranger here as a self-signed one.
//
// And it expects the certificate to be the named node's, whatever address the
// request is sent to. A node is reached by an address, and its certificate
// names it by its own name and by whichever addresses it had when it was
// installed; the name is the part that is always there.
func Client(authority, node string, timeout time.Duration) (*http.Client, error) {
	if strings.TrimSpace(node) == "" {
		return nil, errors.New("no node was named, so there is nobody for the hypervisor's certificate to be")
	}
	pem, err := base64.StdEncoding.DecodeString(strings.TrimSpace(authority))
	if err != nil {
		return nil, errors.New("the hypervisor's authority is not base64, which is how the hypervisor phase stores it")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("the hypervisor's authority does not hold a certificate")
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs:    pool,
			ServerName: node,
			MinVersion: tls.VersionTLS13,
		}},
	}, nil
}
