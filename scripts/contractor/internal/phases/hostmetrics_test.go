package phases

import (
	"crypto/tls"
	"strings"
	"testing"
	"time"

	"homelab/contractor/internal/run"
	"homelab/details/onepassword"
)

// vaultOf is a vault holding the given fields of the host's scrape
// credentials, and what was written to it.
type vaultOf struct {
	has     map[string]bool
	written map[string]string
	item    string
}

func (v *vaultOf) probe(ref string) onepassword.Status {
	if v.has[ref[strings.LastIndex(ref, "/")+1:]] {
		return onepassword.StatusOK
	}
	return onepassword.StatusMissing
}

func (v *vaultOf) write(vault, title string, fields map[string]string) ([]string, error) {
	v.item, v.written = vault+"/"+title, fields
	return nil, nil
}

func ensureIn(v *vaultOf) error {
	return ensureHostMetrics(&run.Context{Site: "site0"}, v.probe, v.write, time.Now())
}

func TestASiteWithNoScrapeCredentialsIsGivenAWholeSet(t *testing.T) {
	v := &vaultOf{}
	if err := ensureIn(v); err != nil {
		t.Fatal(err)
	}
	if v.item != "site0/host_metrics" {
		t.Errorf("written to %q", v.item)
	}
	// The halves that were written are each other's: a set, not five values.
	if _, err := tls.X509KeyPair([]byte(v.written["certificate"]), []byte(v.written["private_key"])); err != nil {
		t.Errorf("the exporter's key is not its certificate's: %v", err)
	}
	if _, err := tls.X509KeyPair([]byte(v.written["scraper_certificate"]), []byte(v.written["scraper_private_key"])); err != nil {
		t.Errorf("the scraper's key is not its certificate's: %v", err)
	}
	if !strings.Contains(v.written["authority"], "CERTIFICATE") {
		t.Error("no authority was written")
	}
	for name, value := range v.written {
		if value != strings.TrimSpace(value) {
			t.Errorf("%s ends in whitespace, which a field does not give back, so the write would not verify", name)
		}
	}
}

func TestASiteWithAWholeSetIsLeftAlone(t *testing.T) {
	v := &vaultOf{has: map[string]bool{"authority": true, "certificate": true, "private_key": true, "scraper_certificate": true, "scraper_private_key": true}}
	if err := ensureIn(v); err != nil {
		t.Fatal(err)
	}
	if v.written != nil {
		t.Error("a site's credentials were replaced under the hypervisor and the cluster holding them")
	}
}

// One missing field cannot be made again to match the rest, and making the
// rest again would leave two halves of different sets in use.
func TestAPartialSetIsRefusedAndNothingIsWritten(t *testing.T) {
	v := &vaultOf{has: map[string]bool{"authority": true, "certificate": true, "private_key": true, "scraper_certificate": true}}
	err := ensureIn(v)
	if err == nil || !strings.Contains(err.Error(), "scraper_private_key") {
		t.Fatalf("got %v, want a refusal naming the missing field", err)
	}
	if v.written != nil {
		t.Error("something was written over a partial set")
	}
}
