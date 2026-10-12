package main

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"
)

// renewWithin is how long before a certificate runs out a new one is issued.
//
// A certificate is good for forty-five days and the notary is run monthly,
// so one found with less than twenty days left would run out before the next
// run. The fortnight between a missed run and a stopped site is the slack the
// operator has to notice in.
const renewWithin = 20 * 24 * time.Hour

// due says why a site is owed a certificate, or "" when the one it holds is
// current: for exactly the names it serves under, with long enough left.
//
// A certificate that cannot be read is an error and not a reason to issue.
// It was written here, so one that does not parse is a vault somebody has
// changed, and issuing over it would hide that.
func due(held string, want []string, now time.Time) (string, error) {
	if len(want) == 0 {
		return "", errors.New("it is granted names to serve under and the list is empty")
	}
	if held == "" {
		return "it holds none", nil
	}
	leaf, err := leafOf(held)
	if err != nil {
		return "", fmt.Errorf("the certificate it holds cannot be read: %w", err)
	}
	have, wanted := slices.Clone(leaf.DNSNames), slices.Clone(want)
	sort.Strings(have)
	sort.Strings(wanted)
	if !slices.Equal(have, wanted) {
		return "the one it holds is for other names", nil
	}
	if left := leaf.NotAfter.Sub(now); left < renewWithin {
		return fmt.Sprintf("the one it holds has %d day(s) left", max(0, int(left.Hours()/24))), nil
	}
	return "", nil
}

// leafOf is the first certificate of a chain as the vault keeps one: PEM,
// base64-encoded onto one line, because a value of more than one line cannot
// be carried by the config a site renders.
func leafOf(held string) (*x509.Certificate, error) {
	raw, err := base64.StdEncoding.DecodeString(held)
	if err != nil {
		return nil, errors.New("it is not base64")
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("it holds no certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}
