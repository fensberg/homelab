package phases

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"homelab/contractor/internal/run"
	"homelab/details/secrets"
)

// vaultOf is a vault holding the given fields of the host's scrape
// credentials, and what was written to it.
type vaultOf struct {
	has     map[string]string
	written map[string]string
	item    string
}

func (v *vaultOf) read(ref string) (string, error) {
	if value, ok := v.has[ref[strings.LastIndex(ref, "/")+1:]]; ok {
		return value, nil
	}
	return "", errors.New("no such field")
}

func (v *vaultOf) write(vault, title string, fields map[string]string) ([]string, error) {
	v.item, v.written = vault+"/"+title, fields
	return nil, nil
}

func ensureIn(v *vaultOf) error {
	return ensureHostMetrics(&run.Context{Site: "site0"}, v.read, v.write, time.Now())
}

func decoded(t *testing.T, value string) []byte {
	t.Helper()
	out, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatalf("not base64: %v", err)
	}
	return out
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
	if _, err := tls.X509KeyPair(decoded(t, v.written["certificate"]), decoded(t, v.written["private_key"])); err != nil {
		t.Errorf("the exporter's key is not its certificate's: %v", err)
	}
	if _, err := tls.X509KeyPair(decoded(t, v.written[secrets.ScraperCertificateField]), decoded(t, v.written[secrets.ScraperPrivateKeyField])); err != nil {
		t.Errorf("the scraper's key is not its certificate's: %v", err)
	}
	if !strings.Contains(string(decoded(t, v.written["authority"])), "CERTIFICATE") {
		t.Error("no authority was written")
	}
}

// The config is a JSON template, and a vault's value is put into it exactly
// as it is. The first version of this stored PEM blocks, whose line breaks
// made the rendered config unparseable and stopped every verb that renders.
// So each value is substituted here the way the render does it, and the
// result has to be JSON that gives the value back.
func TestEveryScrapeCredentialSurvivesBeingSubstitutedIntoTheConfig(t *testing.T) {
	v := &vaultOf{}
	if err := ensureIn(v); err != nil {
		t.Fatal(err)
	}
	if len(v.written) == 0 {
		t.Fatal("nothing was written, so nothing was checked")
	}
	for name, value := range v.written {
		rendered := strings.Replace(`{"field": "{{ the reference }}"}`, "{{ the reference }}", value, 1)
		var doc struct {
			Field string `json:"field"`
		}
		if err := json.Unmarshal([]byte(rendered), &doc); err != nil {
			t.Errorf("%s, substituted into the config as the vault holds it, stops the config parsing: %v", name, err)
			continue
		}
		if doc.Field != value {
			t.Errorf("%s does not come back out of the config as it went in", name)
		}
	}
}

func wholeSet(t *testing.T) map[string]string {
	t.Helper()
	v := &vaultOf{}
	if err := ensureIn(v); err != nil {
		t.Fatal(err)
	}
	return v.written
}

func TestASiteWithAWholeSetIsLeftAlone(t *testing.T) {
	v := &vaultOf{has: wholeSet(t)}
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
	has := wholeSet(t)
	delete(has, secrets.ScraperPrivateKeyField)
	v := &vaultOf{has: has}
	err := ensureIn(v)
	if err == nil || !strings.Contains(err.Error(), secrets.ScraperPrivateKeyField) {
		t.Fatalf("got %v, want a refusal naming the missing field", err)
	}
	if v.written != nil {
		t.Error("something was written over a partial set")
	}
}

// A set stored as PEM blocks is whole and is what broke every render. It is
// refused by name, with the command that clears it, before the render can
// meet it.
func TestASetKeptAsPEMBlocksIsRefusedBeforeItReachesTheConfig(t *testing.T) {
	has := wholeSet(t)
	has["authority"] = string(decoded(t, has["authority"]))
	v := &vaultOf{has: has}
	err := ensureIn(v)
	if err == nil || !strings.Contains(err.Error(), "authority") || !strings.Contains(err.Error(), "op item delete host_metrics --vault site0") {
		t.Fatalf("got %v, want a refusal naming the field and how to clear it", err)
	}
	if v.written != nil {
		t.Error("something was written over a malformed set")
	}
}
