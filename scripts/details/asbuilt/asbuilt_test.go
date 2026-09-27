package asbuilt

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net"
	"regexp"
	"strings"
	"testing"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func mustFingerprinter(t *testing.T, b byte) *Fingerprinter {
	t.Helper()
	f, err := NewFingerprinter(key(b))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func mustDecode(t *testing.T, s string) map[string]any {
	t.Helper()
	v, err := Decode([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// A short key makes every fingerprint a hash anybody can test guesses
// against.
func TestAShortFingerprintKeyIsRefused(t *testing.T) {
	if _, err := NewFingerprinter(make([]byte, 16)); err == nil {
		t.Error("a 16-byte key was accepted")
	}
}

// The same value under the same key agrees with itself, which is what makes a
// record consistent; under another key it does not, which is what makes a
// discarded key worth discarding.
func TestAFingerprintAgreesWithItselfAndDependsOnTheKey(t *testing.T) {
	a, b := mustFingerprinter(t, 1), mustFingerprinter(t, 2)
	if a.Opaque("harbour-road") != a.Opaque("harbour-road") {
		t.Error("one key gave two fingerprints for one value")
	}
	if a.Opaque("harbour-road") == b.Opaque("harbour-road") {
		t.Error("two keys gave the same fingerprint, so the key is not in it")
	}
	if a.Opaque("harbour-road") == a.Opaque("south-street") {
		t.Error("two values gave the same fingerprint")
	}
	if !regexp.MustCompile(`^r[0-9a-f]{12}$`).MatchString(a.Opaque("Harbour Road")) {
		t.Errorf("an opaque stand-in is not a DNS label: %q", a.Opaque("Harbour Road"))
	}
}

// Each shape the code depends on survives fingerprinting, and nothing of the
// real value does.
func TestShapedFieldsKeepTheirShapeAndLoseTheirValue(t *testing.T) {
	f := mustFingerprinter(t, 1)

	ip := f.Field("ip", "192.0.2.10")
	parsed := net.ParseIP(ip)
	_, bench, _ := net.ParseCIDR("198.18.0.0/15")
	if parsed == nil || !bench.Contains(parsed) {
		t.Errorf("an address became %q, which is not an address in the benchmarking range", ip)
	}

	url := f.Field("repo_url", "https://git.example/owner/repo")
	if strings.Contains(url, "owner") || strings.Contains(url, "git.example") {
		t.Errorf("the url stand-in %q still holds the real url", url)
	}
	if got := strings.Split(url, "/"); len(got) != 5 || got[0] != "https:" {
		t.Errorf("the url stand-in %q does not split like a url", url)
	}

	tok := f.Field("token_id", "root@pam!tofu")
	if !regexp.MustCompile(`^r[0-9a-f]{12}@r[0-9a-f]{12}!r[0-9a-f]{12}$`).MatchString(tok) {
		t.Errorf("the token id stand-in %q is not user@realm!name", tok)
	}

	secret := f.Field("token_secret", "11111111-2222-3333-4444-555555555555")
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(secret) {
		t.Errorf("the token secret stand-in %q is not a uuid", secret)
	}

	if _, err := base64.StdEncoding.DecodeString(f.Field("private_key", "a-real-key")); err != nil {
		t.Errorf("the private key stand-in is not base64: %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(f.Field("account_id", "acct")) {
		t.Errorf("the account id stand-in is not 32 hex characters")
	}
	if f.Field("hostname", "pve1") == "pve1" || !strings.HasPrefix(f.Field("hostname", "pve1"), "r") {
		t.Error("an unshaped field was not replaced with an opaque stand-in")
	}
}

// A url without a scheme is not taken apart as if it had one.
func TestAURLWithoutASchemeIsOpaque(t *testing.T) {
	f := mustFingerprinter(t, 1)
	if got := f.Field("webhook_url", "not a url"); got != f.Opaque("not a url") {
		t.Errorf("got %q", got)
	}
}

// Only what the template says the vault filled in is replaced. A literal in
// the template is public, and a vendor attestation is compared by
// preconditions, so both are kept.
func TestTheVaultWalkReplacesOnlyWhatTheTemplateReferences(t *testing.T) {
	f, r := mustFingerprinter(t, 1), NewReplacements()
	var tpl any
	_ = json.Unmarshal([]byte(`{
		"sites": {"site0": {
			"name": "{{ op://site0-shared/identity/name }}",
			"octet": 10,
			"hypervisor": {"provider": "proxmox", "vault_provider": "{{ op://site0/hypervisor/provider }}"}
		}},
		"missing": "{{ op://site0/x/y }}"
	}`), &tpl)
	cfg := mustDecode(t, `{
		"sites": {"site0": {
			"name": "Harbour Road",
			"octet": 10,
			"hypervisor": {"provider": "proxmox", "vault_provider": "proxmox"}
		}},
		"undeclared": "kept"
	}`)

	out := Vault(f, r, tpl, cfg).(map[string]any)
	site := out["sites"].(map[string]any)["site0"].(map[string]any)
	if site["name"] == "Harbour Road" || site["name"] != f.Opaque("Harbour Road") {
		t.Errorf("the vault-sourced name became %v", site["name"])
	}
	if site["octet"] != json.Number("10") {
		t.Errorf("a template literal changed: %v", site["octet"])
	}
	hv := site["hypervisor"].(map[string]any)
	if hv["vault_provider"] != "proxmox" {
		t.Errorf("an attestation was fingerprinted: %v", hv["vault_provider"])
	}
	if out["undeclared"] != "kept" {
		t.Error("a key the template does not declare was changed")
	}

	secrets := r.Secrets()
	for _, form := range []string{"Harbour Road", "harbour road", "harbour-road", base64.StdEncoding.EncodeToString([]byte("Harbour Road"))} {
		if _, ok := secrets[form]; !ok {
			t.Errorf("the form %q is not registered, so a copy stored that way would survive", form)
		}
	}
	if _, ok := secrets["proxmox"]; ok {
		t.Error("an attestation was registered as a secret")
	}
}

// Longest first: a value containing another is replaced whole, not with the
// shorter one's stand-in spliced into it.
func TestReplacementIsLongestFirstAndShortValuesOnlyWhole(t *testing.T) {
	r := NewReplacements()
	r.Add("vault:a", "north", "AAAA")
	r.Add("vault:b", "harbour-road", "BBBB")
	r.Add("vault:c", "10", "99")
	sub := r.replacer()
	if got := sub("host.harbour-road.lan"); got != "host.BBBB.lan" {
		t.Errorf("got %q", got)
	}
	if got := sub("10"); got != "99" {
		t.Errorf("a short value that is the whole string was not replaced: %q", got)
	}
	if got := sub("10.10.0.1"); got != "10.10.0.1" {
		t.Errorf("a short value was replaced inside a longer string: %q", got)
	}
}

// A value marked sensitive only by where it sits is replaced where it is the
// whole string, never inside another one - or every resource named after it
// would change.
func TestAWholeValueIsNotReplacedInsideAnother(t *testing.T) {
	r := NewReplacements()
	r.AddWhole("sensitive", "database", "rstandin")
	r.AddWhole("sensitive", "database", "rsecond") // the first registration wins
	sub := r.replacer()
	if sub("database") != "rstandin" {
		t.Errorf("the whole value was not replaced: %q", sub("database"))
	}
	if sub("database-primary") != "database-primary" {
		t.Errorf("a whole-only value was replaced inside another string: %q", sub("database-primary"))
	}
	if !r.Secrets()["database"].Whole {
		t.Error("the scan would look for a whole-only value inside other strings")
	}
}

const priorPlan = `{
  "prior_state": {"values": {
    "root_module": {
      "resources": [
        {"address": "random_password.db", "values": {"result": "hunter2hunter2", "length": 14},
         "sensitive_values": {"result": true}},
        {"address": "kubernetes_secret.s", "values": {"data": {"user": "app", "port": "5432", "secret": "fixture-long-value"}},
         "sensitive_values": {"data": true}}
      ],
      "child_modules": [
        {"resources": [{"values": {"list": ["a-secret-in-a-list"]}, "sensitive_values": {"list": [true]}}]}
      ]
    },
    "outputs": {"conn": {"sensitive": true, "value": "scheme://app:fixture-long-value@db"}, "open": {"sensitive": false, "value": "public-value"}}
  }}
}`

// Every value the plan marks sensitive is registered - including one marked
// by schema, which the state file's own list does not carry - and a value
// marked only because its container is (a port) is not.
func TestSensitiveLeavesCoverSchemaMarksModulesAndOutputs(t *testing.T) {
	f, r := mustFingerprinter(t, 1), NewReplacements()
	if err := SensitiveLeaves(f, r, mustDecode(t, priorPlan)); err != nil {
		t.Fatal(err)
	}
	s := r.Secrets()
	for _, want := range []string{"hunter2hunter2", "fixture-long-value", "a-secret-in-a-list", "scheme://app:fixture-long-value@db"} {
		if _, ok := s[want]; !ok {
			t.Errorf("%q is marked sensitive and was not registered", want)
		}
	}
	for _, not := range []string{"5432", "app", "public-value"} {
		if _, ok := s[not]; ok {
			t.Errorf("%q was registered, though it is trivial, short or not sensitive", not)
		}
	}
}

func TestAPlanWithoutPriorStateIsAnError(t *testing.T) {
	if err := SensitiveLeaves(mustFingerprinter(t, 1), NewReplacements(), map[string]any{}); err == nil {
		t.Error("a plan with no prior state was accepted, so nothing would have been marked secret")
	}
}

// A certificate or key is replaced with one of the same kind, bare or
// base64-encoded, because the Kubernetes provider parses them at configure.
func TestACertificateOrKeyIsReplacedWithOneOfTheSameKind(t *testing.T) {
	f := mustFingerprinter(t, 1)
	cert := throwawayPEM("certificate")
	keyPEM := throwawayPEM("key")
	if pemKind(cert) != "certificate" {
		t.Error("the throwaway certificate is not a certificate")
	}
	if got := standInFor(f, cert); pemKind(got) != "certificate" {
		t.Errorf("a certificate was replaced with a %q", pemKind(got))
	}
	if pemKind(keyPEM) != "key" {
		t.Error("the throwaway key is not a key")
	}
	if got := standInFor(f, keyPEM); pemKind(got) != "key" {
		t.Errorf("a key was replaced with a %q", pemKind(got))
	}
	enc := base64.StdEncoding.EncodeToString([]byte(cert))
	got, err := base64.StdEncoding.DecodeString(standInFor(f, enc))
	if err != nil || pemKind(string(got)) != "certificate" {
		t.Error("a base64 certificate was not replaced with a base64 certificate")
	}
	if standInFor(f, "plain-secret") != f.Opaque("plain-secret") {
		t.Error("a plain secret did not get an opaque stand-in")
	}
}

const machineState = `{
  "serial": 7,
  "resources": [
    {"mode": "managed", "type": "talos_machine_secrets", "name": "this",
     "instances": [{"attributes": {"machine_secrets": {"certs": {"os": {"cert": "REAL-OS-CA-CERT", "key": "REAL-OS-CA-KEY"}}}, "list": ["REAL-TOKEN-1"]}}]},
    {"mode": "managed", "type": "talos_machine_configuration_apply", "name": "cp",
     "instances": [{"index_key": "pve-harbour-road", "private": "` + "UkVBTC1PUy1DQS1LRVk=" + `",
       "attributes": {"input": "ca: REAL-OS-CA-CERT\nkey: REAL-OS-CA-KEY\n", "node": "harbour-road"}}]}
  ],
  "outputs": {"talosconfig": {"value": "cert: REAL-OS-CA-CERT", "type": "string", "sensitive": true}}
}`

// The real CA is swapped for the throwaway one, and every copy of it
// elsewhere - a machine config, an output, provider data - is replaced with
// the throwaway value from the same position.
func TestTheRealCAIsReplacedEverywhereItIsCopied(t *testing.T) {
	state := mustDecode(t, machineState)
	throw := mustDecode(t, `{"machine_secrets": {"certs": {"os": {"cert": "THROW-CERT", "key": "THROW-KEY"}}}, "list": ["THROW-TOKEN"]}`)
	r := NewReplacements()
	r.Add("vault:name", "harbour-road", "rstandin")
	if n, err := SwapMachineSecrets(state, throw, r); err != nil || n != 1 {
		t.Fatalf("swapped %d, %v", n, err)
	}
	Scrub(state, r)

	b, _ := json.Marshal(state)
	for _, real := range []string{"REAL-OS-CA-CERT", "REAL-OS-CA-KEY", "REAL-TOKEN-1", "harbour-road"} {
		if bytes.Contains(b, []byte(real)) {
			t.Errorf("%q is still in the record", real)
		}
	}
	if !bytes.Contains(b, []byte("ca: THROW-CERT")) {
		t.Error("the machine config's copy of the CA was not replaced with the throwaway one")
	}
	res := state["resources"].([]any)[1].(map[string]any)
	inst := res["instances"].([]any)[0].(map[string]any)
	if inst["index_key"] != "pve-rstandin" {
		t.Errorf("an instance key holding a vault value became %v", inst["index_key"])
	}
	priv, _ := base64.StdEncoding.DecodeString(inst["private"].(string))
	if string(priv) != "THROW-KEY" {
		t.Errorf("provider private data was not scrubbed inside its encoding: %q", priv)
	}
	if len(Scan(state, r.Secrets())) != 0 {
		t.Errorf("the scan still finds: %v", Scan(state, r.Secrets()))
	}
}

func TestAStateWithoutMachineSecretsIsNotASite(t *testing.T) {
	if _, err := SwapMachineSecrets(mustDecode(t, `{"resources": []}`), map[string]any{}, NewReplacements()); err == nil {
		t.Error("a state with no CA was accepted as a site's")
	}
}

// The scan finds a real value wherever it survived and reports only where:
// never the value, and never an instance key.
func TestTheScanReportsWhereAndNeverWhat(t *testing.T) {
	state := mustDecode(t, `{"resources": [
	  {"mode": "managed", "type": "proxmox_vm", "name": "cp", "instances": [
	    {"index_key": "pve-harbour-road", "attributes": {"description": "built at harbour-road", "name": "database"}}]},
	  {"mode": "data", "type": "talos_config", "name": "c", "instances": [{"attributes": {"x": "database-primary"}}]}
	], "outputs": {"o": {"value": "harbour-road"}}}`)
	secrets := map[string]Secret{
		"harbour-road": {Source: "vault:name"},
		"database":     {Source: "sensitive", Whole: true},
	}
	var got []string
	for _, f := range Scan(state, secrets, map[string]any{"a": "database", "b": "harbour-road"}) {
		got = append(got, f.String())
		if strings.Contains(f.Where, "pve-") {
			t.Errorf("a finding names an instance key: %q", f.Where)
		}
	}
	want := []string{
		"config document 1  (vault:name)",
		"output.o  (vault:name)",
		"proxmox_vm.cp (instance key)  (vault:name)",
		"proxmox_vm.cp.description  (vault:name)",
		"proxmox_vm.cp.name  (sensitive)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("findings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// What an offline plan disagrees with is folded in: known values from the
// plan, unknown ones kept, dynamic wrappers kept, sensitivity marks matched,
// outputs updated and the serial moved on.
func TestFoldingTakesThePlansKnownValuesAndKeepsTheRest(t *testing.T) {
	state := mustDecode(t, `{"serial": 3, "resources": [
	  {"mode": "managed", "type": "kubernetes_secret", "name": "s", "instances": [
	    {"attributes": {"data": {"k": "old"}, "id": "computed-id", "nested": {"keep": "yes", "set": "old"}, "dyn": {"value": "old", "type": "string"}}}]},
	  {"module": "module.m", "mode": "managed", "type": "t", "name": "n", "instances": [{"index_key": 0, "attributes": {"a": "old"}}]}
	], "outputs": {"o": {"value": "old", "type": "string", "sensitive": false}}}`)
	plan := mustDecode(t, `{
	  "resource_changes": [
	    {"mode": "managed", "type": "kubernetes_secret", "name": "s",
	     "change": {"actions": ["update"], "after": {"data": {"k": "new"}, "nested": {"set": "new"}, "dyn": "new"},
	                "after_unknown": {"id": true, "nested": {"keep": true}}, "after_sensitive": {"data": true}}},
	    {"module_address": "module.m", "mode": "managed", "type": "t", "name": "n", "index": 0,
	     "change": {"actions": ["update"], "after": {"a": "new"}, "after_unknown": {}, "after_sensitive": {}}},
	    {"mode": "managed", "type": "t", "name": "created", "change": {"actions": ["create"], "after": {}}},
	    {"mode": "data", "type": "d", "name": "r", "change": {"actions": ["read"]}},
	    {"mode": "managed", "type": "t", "name": "quiet", "change": {"actions": ["no-op"]}}
	  ],
	  "output_changes": {"o": {"actions": ["update"], "after": "new", "after_sensitive": true},
	                     "gone": {"actions": ["create"], "after": "x"},
	                     "later": {"actions": ["update"], "after_unknown": true}}
	}`)
	n, err := Fold(state, plan)
	if err != nil || n != 3 {
		t.Fatalf("folded %d, %v", n, err)
	}
	attrs := state["resources"].([]any)[0].(map[string]any)["instances"].([]any)[0].(map[string]any)
	a := attrs["attributes"].(map[string]any)
	if a["data"].(map[string]any)["k"] != "new" {
		t.Error("a value the plan knows was not taken")
	}
	if a["id"] != "computed-id" {
		t.Errorf("a value the plan cannot know was dropped: %v", a["id"])
	}
	if nested := a["nested"].(map[string]any); nested["keep"] != "yes" || nested["set"] != "new" {
		t.Errorf("a nested unknown was not kept beside a nested known: %v", nested)
	}
	if dyn := a["dyn"].(map[string]any); dyn["value"] != "new" || dyn["type"] != "string" {
		t.Errorf("a dynamic attribute lost its wrapper: %v", dyn)
	}
	marks, _ := json.Marshal(attrs["sensitive_attributes"])
	if string(marks) != `[[{"type":"get_attr","value":"data"}]]` {
		t.Errorf("sensitivity marks are %s", marks)
	}
	moduled := state["resources"].([]any)[1].(map[string]any)["instances"].([]any)[0].(map[string]any)["attributes"].(map[string]any)
	if moduled["a"] != "new" {
		t.Error("a resource in a module was not matched to its change")
	}
	o := state["outputs"].(map[string]any)["o"].(map[string]any)
	if o["value"] != "new" || o["sensitive"] != true {
		t.Errorf("the output became %v", o)
	}
	if state["serial"] != json.Number("4") {
		t.Errorf("the serial is %v, so tofu would refuse the record as older than itself", state["serial"])
	}
}

func TestSensitivePathsIndexLists(t *testing.T) {
	got, _ := json.Marshal(sensitivePaths([]any{false, true}, nil, []any{}))
	if string(got) != `[[{"type":"index","value":{"type":"number","value":1}}]]` {
		t.Errorf("got %s", got)
	}
}

func TestFoldingRefusesASerialThatIsNotANumber(t *testing.T) {
	if _, err := Fold(map[string]any{"serial": json.Number("x")}, map[string]any{}); err == nil {
		t.Error("a broken serial was accepted")
	}
}

func TestMergeKeepsTheOldValueWhereTheWholeListIsUnknown(t *testing.T) {
	if got := merge([]any{"a"}, nil, []any{true}); len(got.([]any)) != 1 {
		t.Errorf("got %v", got)
	}
	if got := merge(map[string]any{"x": "y"}, nil, map[string]any{"x": true}); got.(map[string]any)["x"] != "y" {
		t.Errorf("got %v", got)
	}
	if got := merge("old", nil, false); got != nil {
		t.Errorf("a value the plan says is now null was kept: %v", got)
	}
}

func TestDNSLabelMatchesTheClusterRootsSanitising(t *testing.T) {
	for in, want := range map[string]string{"Harbour Road Office": "harbour-road-office", "--A__b--": "a-b", "": ""} {
		if got := dnsLabel(in); got != want {
			t.Errorf("dnsLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
