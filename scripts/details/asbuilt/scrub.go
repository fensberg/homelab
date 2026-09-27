package asbuilt

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
)

// instances visits every resource instance in a state document.
func instances(state map[string]any, visit func(resource, instance map[string]any)) {
	resources, _ := state["resources"].([]any)
	for _, r := range resources {
		res, _ := r.(map[string]any)
		insts, _ := res["instances"].([]any)
		for _, i := range insts {
			if inst, ok := i.(map[string]any); ok {
				visit(res, inst)
			}
		}
	}
}

// SwapMachineSecrets puts a throwaway set of Talos machine secrets in place of
// the real ones, and registers each real value against the throwaway value in
// the same position.
//
// Position matters because the machine configs embed the same values, so the
// replacement has to be one consistent substitution rather than a fresh
// random value per occurrence. And throwaway rather than fingerprinted
// because Talos parses these - they have to be real keys of the right kind,
// just not ones anything trusts.
func SwapMachineSecrets(state, throwaway map[string]any, r *Replacements) (int, error) {
	swapped := 0
	instances(state, func(res, inst map[string]any) {
		if res["type"] != "talos_machine_secrets" || res["mode"] != "managed" {
			return
		}
		pair(r, inst["attributes"], throwaway)
		inst["attributes"] = throwaway
		swapped++
	})
	if swapped == 0 {
		return 0, fmt.Errorf("the state holds no talos_machine_secrets, so there is no CA to replace and this is not the state of a site")
	}
	return swapped, nil
}

func pair(r *Replacements, real, stand any) {
	switch t := real.(type) {
	case string:
		if s, ok := stand.(string); ok {
			r.Add(SourceMachineSecrets, t, s)
		}
	case map[string]any:
		s, _ := stand.(map[string]any)
		for k, v := range t {
			pair(r, v, s[k])
		}
	case []any:
		s, _ := stand.([]any)
		for i, v := range t {
			if i < len(s) {
				pair(r, v, s[i])
			}
		}
	}
}

// trivial is a value marked sensitive only because it sits inside something
// that is: a port, a flag, a count. Replacing it would corrupt the record for
// nothing, and the scan does not look for it.
var trivial = regexp.MustCompile(`^([0-9]+|true|false|null)$`)

// SensitiveLeaves registers a stand-in for every value a plan's prior state
// marks sensitive, beyond those already registered.
//
// Read from `tofu show -json` of a plan rather than from the state file,
// because the plan's marks include the ones a provider's schema declares, and
// the state file's list does not: a random password's result is sensitive by
// schema and would otherwise be missed.
func SensitiveLeaves(f *Fingerprinter, r *Replacements, plan map[string]any) error {
	prior, _ := plan["prior_state"].(map[string]any)
	values, _ := prior["values"].(map[string]any)
	if values == nil {
		return fmt.Errorf("the plan carries no prior state, so nothing here can say which values are secret")
	}
	var module func(m map[string]any)
	module = func(m map[string]any) {
		resources, _ := m["resources"].([]any)
		for _, x := range resources {
			res, _ := x.(map[string]any)
			marked(f, r, res["values"], res["sensitive_values"])
		}
		children, _ := m["child_modules"].([]any)
		for _, c := range children {
			if cm, ok := c.(map[string]any); ok {
				module(cm)
			}
		}
	}
	if root, ok := values["root_module"].(map[string]any); ok {
		module(root)
	}
	outputs, _ := values["outputs"].(map[string]any)
	for _, o := range outputs {
		out, _ := o.(map[string]any)
		if out["sensitive"] == true {
			marked(f, r, out["value"], true)
		}
	}
	return nil
}

func marked(f *Fingerprinter, r *Replacements, value, mark any) {
	if mark == true {
		leaves(value, func(s string) {
			if len(s) < minSubstring || trivial.MatchString(s) || r.Has(s) {
				return
			}
			r.AddWhole(SourceSensitive, s, standInFor(f, s))
		})
		return
	}
	switch m := mark.(type) {
	case map[string]any:
		v, _ := value.(map[string]any)
		for k, sub := range m {
			marked(f, r, v[k], sub)
		}
	case []any:
		v, _ := value.([]any)
		for i, sub := range m {
			if i < len(v) {
				marked(f, r, v[i], sub)
			}
		}
	}
}

func leaves(v any, visit func(string)) {
	switch t := v.(type) {
	case string:
		visit(t)
	case map[string]any:
		for _, x := range t {
			leaves(x, visit)
		}
	case []any:
		for _, x := range t {
			leaves(x, visit)
		}
	}
}

// standInFor keeps the one shape that is parsed at plan time: a PEM
// certificate or key, bare or base64-encoded. The Kubernetes provider decodes
// the client credentials it is configured with, so an opaque token there
// fails the plan before it starts.
func standInFor(f *Fingerprinter, s string) string {
	if kind := pemKind(s); kind != "" {
		return throwawayPEM(kind)
	}
	if raw, err := base64.StdEncoding.DecodeString(s); err == nil {
		if kind := pemKind(string(raw)); kind != "" {
			return base64.StdEncoding.EncodeToString([]byte(throwawayPEM(kind)))
		}
	}
	return f.Opaque(s)
}

func pemKind(s string) string {
	block, _ := pem.Decode([]byte(strings.TrimSpace(s)))
	if block == nil {
		return ""
	}
	if strings.Contains(block.Type, "PRIVATE KEY") {
		return "key"
	}
	return "certificate"
}

// throwaway is one self-signed pair per process: every stand-in certificate
// matches every stand-in key, which is all a provider checks when it
// configures, and none of it is trusted by anything.
var throwaway = struct {
	cert, key string
}{}

func throwawayPEM(kind string) string {
	if throwaway.cert == "" {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			panic(err) // the system's randomness failing is not recoverable here
		}
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(1),
			Subject:      pkix.Name{CommonName: "as-built throwaway"},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().AddDate(10, 0, 0),
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
		if err != nil {
			panic(err)
		}
		kder, err := x509.MarshalECPrivateKey(k)
		if err != nil {
			panic(err)
		}
		throwaway.cert = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
		throwaway.key = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}))
	}
	if kind == "key" {
		return throwaway.key
	}
	return throwaway.cert
}

// Scrub applies the replacements to everything in the state that describes
// the estate: attributes, instance keys and outputs. Structural fields -
// resource types, provider addresses, the lineage - are left alone, since
// they are public vocabulary and rewriting them would make the file something
// tofu no longer reads.
func Scrub(state map[string]any, r *Replacements) {
	sub := r.replacer()
	instances(state, func(_, inst map[string]any) {
		inst["attributes"] = rewrite(inst["attributes"], sub)
		if k, ok := inst["index_key"].(string); ok {
			inst["index_key"] = sub(k)
		}
		// private is provider data, base64 of whatever the provider chose to
		// keep. Kept rather than dropped, because the spike saw providers plan
		// changes from it; scrubbed inside its encoding, and the scan reads it
		// the same way.
		if p, ok := inst["private"].(string); ok {
			if raw, err := base64.StdEncoding.DecodeString(p); err == nil {
				inst["private"] = base64.StdEncoding.EncodeToString([]byte(sub(string(raw))))
			}
		}
	})
	if outputs, ok := state["outputs"].(map[string]any); ok {
		for _, o := range outputs {
			if out, ok := o.(map[string]any); ok {
				out["value"] = rewrite(out["value"], sub)
			}
		}
	}
}
