package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	cf "homelab/details/cloudflare"
	"homelab/details/console"
	"homelab/details/onepassword"
	"homelab/details/repopath"
	"homelab/details/secrets"
	"homelab/details/stateencryption"
	"homelab/details/tofustate"
	"homelab/details/vaults"
)

// enrollmentAddress is the one estate object that may already exist before
// the estate is built: Cloudflare keeps a single WARP enrollment application
// per account and refuses a second.
const enrollmentAddress = "cloudflare_zero_trust_access_application.enrollment"

// config is config/estate.rendered.json.
type config struct {
	Organization struct {
		Name string `json:"name"`
	} `json:"organization"`
	// The plots the estate grants, one per site key, with each site's name.
	Plots map[string]struct {
		Name string `json:"name"`
	} `json:"plots"`

	// Who may enroll a device and what it reaches, and the credential that
	// administers both. Named for the function; Provider says whose it is.
	Access struct {
		Provider      string `json:"provider"`
		VaultProvider string `json:"vault_provider"`
		AccountID     string `json:"account_id"`
		APIToken      string `json:"api_token"`
		Members       string `json:"members"`
	} `json:"access"`
	State struct {
		Bucket          string `json:"bucket"`
		AccessKeyID     string `json:"access_key_id"`
		SecretAccessKey string `json:"secret_access_key"`
	} `json:"state"`
}

// validate names the first field that would make a run fail later and further
// from its cause. Field names only: a value never reaches the log.
func (c config) validate() error {
	if c.Access.Provider != "cloudflare" {
		return fmt.Errorf("access.provider is %q; management/estate/ implements cloudflare", c.Access.Provider)
	}
	if strings.TrimSpace(c.Access.VaultProvider) != "cloudflare" {
		return errors.New("op://estate/access/provider attests a provider other than cloudflare, so its credentials may belong to another vendor")
	}
	for field, v := range map[string]string{
		"access/account_id":       c.Access.AccountID,
		"access/api_token":        c.Access.APIToken,
		"access/members":          c.Access.Members,
		"state/bucket":            c.State.Bucket,
		"state/access_key_id":     c.State.AccessKeyID,
		"state/secret_access_key": c.State.SecretAccessKey,
	} {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("%s in the estate vault is empty", field)
		}
	}
	if strings.TrimSpace(c.Organization.Name) == "" {
		return errors.New("organization/name in the estate-shared vault is empty, and it names every bucket the estate creates")
	}
	if len(c.Plots) == 0 {
		return errors.New("config/estate.tpl.json lists no plots, so the estate would grant nothing")
	}
	for key, site := range c.Plots {
		if strings.TrimSpace(site.Name) == "" {
			return fmt.Errorf("%s/name in the estate vault is empty, and it names the site's buckets", key)
		}
	}
	return nil
}

// admit decides whether a verb may run against what the estate's state holds.
// Build establishes and converge maintains; each refuses the other's case, so
// a converge can never quietly build an estate from nothing, and a build can
// never be run twice over one that stands.
func admit(verb string, resources []string) error {
	switch verb {
	case "build-estate":
		if len(resources) > 0 {
			return fmt.Errorf("the estate stands: its state holds %d resource(s). Use converge-estate", len(resources))
		}
	case "converge-estate", "demolish-estate":
		if len(resources) == 0 {
			return errors.New("there is no estate: its state is empty. Use build-estate")
		}
	}
	return nil
}

type estate struct {
	dir      string // management/estate
	tpl      string
	rendered string
	cfg      config

	// run executes tofu in the estate root. A field so the tests can see
	// what the lawyer would have asked tofu to do.
	run func(args ...string) error
}

func execute(verb string) error {
	root, err := repopath.Root()
	if err != nil {
		return err
	}
	e := &estate{
		dir:      filepath.Join(root, "management", "estate"),
		tpl:      filepath.Join(root, "config", "estate.tpl.json"),
		rendered: filepath.Join(root, "config", "estate.rendered.json"),
	}
	e.run = e.tofu
	// Always, however the run ends: the rendered file holds the estate's
	// token and the bucket's credential.
	defer e.sterilize()

	console.Phase("Render", "Pull the estate's secrets from the estate vault.")
	if err := e.vault(); err != nil {
		return err
	}
	if err := e.encrypt(); err != nil {
		return err
	}
	if err := e.render(); err != nil {
		return err
	}

	console.Phase("Take over", "Open the estate's state in its own bucket.")
	if err := e.run("init", "-input=false", "-backend-config=bucket="+e.cfg.State.Bucket); err != nil {
		return err
	}
	resources, err := e.stateList()
	if err != nil {
		return err
	}
	if err := admit(verb, resources); err != nil {
		return err
	}

	api := cloudflare{account: e.cfg.Access.AccountID, token: e.cfg.Access.APIToken}
	if verb == "demolish-estate" {
		console.Phase("Demolish", "Tear the estate down, once no site stands on it.")
		connected, err := api.connectedTunnels()
		if err != nil {
			return err
		}
		if connected > 0 {
			return fmt.Errorf("%d site tunnel(s) have a connector serving them, so a site stands on the estate. Demolish each with `contractor demolish-site` first", connected)
		}
		if err := e.releaseBuckets(); err != nil {
			return err
		}
		return e.run("destroy", "-input=false", "-auto-approve")
	}

	console.Phase("Apply", "Bring the estate to what management/estate/ declares.")
	if err := e.adoptEnrollment(api, resources); err != nil {
		return err
	}
	if err := e.adoptBuckets(api); err != nil {
		return err
	}
	if err := e.run("apply", "-input=false", "-auto-approve"); err != nil {
		return err
	}

	console.Phase("Grant", "Write what each site is granted into its -shared vault.")
	return e.grant()
}

func (e *estate) vault() error {
	if !onepassword.Available() {
		return onepassword.ErrNoCLI
	}
	if !onepassword.SignedIn() {
		return errors.New("1Password is not signed in. The lawyer runs on the estate's service account: export OP_SERVICE_ACCOUNT_TOKEN from the estate token")
	}
	names, err := onepassword.Vaults()
	if err != nil {
		return err
	}
	if err := vaults.CheckLawyer(names); err != nil {
		return err
	}
	console.Ok("the token reaches the estate vault and the vaults it shares, and nothing else")
	return nil
}

// encrypt puts the estate's own passphrase into TF_ENCRYPTION, generating it
// on the first build. The estate's, not any site's: a site's passphrase lives
// in the site's vault, which this token cannot read.
func (e *estate) encrypt() error {
	ref := onepassword.Ref{Vault: vaults.Estate, Item: "state", Field: stateencryption.PassphraseField}
	return stateencryption.Establish(func() (string, error) {
		passphrase, status, err := onepassword.EnsureField(ref, func() (string, error) { return secrets.Password(32) })
		if status == "generated" {
			console.Ok("generated the estate's state encryption passphrase and stored it in the estate vault")
		}
		return passphrase, err
	})
}

func (e *estate) render() error {
	if err := onepassword.Inject(e.tpl, e.rendered); err != nil {
		return err
	}
	if err := os.Chmod(e.rendered, 0o600); err != nil {
		return err
	}
	body, err := os.ReadFile(e.rendered)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, &e.cfg); err != nil {
		return fmt.Errorf("config/estate.rendered.json: %w", err)
	}
	if err := e.cfg.validate(); err != nil {
		return err
	}
	console.Ok("the estate's secrets are rendered")
	return nil
}

// adoptEnrollment takes the account's existing enrollment application into
// state, when there is one and state does not hold it. Cloudflare allows one
// per account, so creating another fails; and one may exist for reasons the
// estate did not cause, since the account creates one with every Zero Trust
// organisation. Found in Go rather than by an import block, because an import
// block fails the whole plan when the application is absent - which is the
// state a demolished estate leaves.
func (e *estate) adoptEnrollment(api cloudflare, resources []string) error {
	for _, r := range resources {
		if r == enrollmentAddress {
			return nil
		}
	}
	id, err := api.enrollmentApp()
	if err != nil {
		return err
	}
	if id == "" {
		console.Info("the account has no enrollment application; the apply creates it")
		return nil
	}
	console.Info("adopting the account's enrollment application")
	return e.run("import", "-input=false", enrollmentAddress, e.cfg.Access.AccountID+"/"+id)
}

// stateList names the managed resources the estate's state holds.
//
// `state pull` rather than `state list`: list fails with "No state file was
// found" on the first build, which is the one run where an empty answer is
// the correct one. Pull answers it - with nothing on the local backend, and
// with a blank, lineage-less state on the S3 backend the estate uses.
func (e *estate) stateList() ([]string, error) {
	out, err := e.tofuOutput("state", "pull")
	if err != nil {
		return nil, err
	}
	return stateResources([]byte(out))
}

// stateResources reads resource addresses out of a pulled state. No state at
// all is no resources. State that is present and cannot be read is an error,
// never an empty estate: build-estate would take it for permission to build.
func stateResources(state []byte) ([]string, error) {
	if len(bytes.TrimSpace(state)) == 0 {
		return nil, nil
	}
	st, err := tofustate.Parse(state)
	if err != nil {
		return nil, fmt.Errorf("the estate's state is present and cannot be read (%s): %w", shape(state), err)
	}
	if st.Version == 0 {
		return nil, fmt.Errorf("the estate's state is present and has no version, so it is not state this lawyer can read. What came back: %s", shape(state))
	}
	// The S3 backend's answer when its bucket holds no state: not nothing, as
	// the local backend gives, but a blank state with no lineage. A lineage is
	// assigned by the first write, so none at all is no state - provided it
	// also claims nothing. A blank lineage beside resources is not a blank
	// state, and is refused rather than read as one.
	if st.Lineage == "" {
		if st.Serial == 0 && len(st.Resources) == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("the estate's state has resources or a serial but no lineage, so it is not state this lawyer can read. What came back: %s", shape(state))
	}
	return st.Managed(), nil
}

// shape says what a pulled state looks like without saying what is in it:
// its size, and its top-level keys when it is a JSON object. Keys are
// OpenTofu's vocabulary; values are the estate's and never printed.
func shape(body []byte) string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return fmt.Sprintf("%d bytes, not a JSON object", len(body))
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return fmt.Sprintf("%d bytes, a JSON object with keys %v", len(body), keys)
}

// backendEnv reaches the estate's bucket with its own credential and no other,
// through the environment of one process so it is never written to disk.
func (e *estate) backendEnv() []string {
	return append(os.Environ(),
		"AWS_ACCESS_KEY_ID="+e.cfg.State.AccessKeyID,
		"AWS_SECRET_ACCESS_KEY="+e.cfg.State.SecretAccessKey,
		"AWS_ENDPOINT_URL_S3="+cf.R2Endpoint(e.cfg.Access.AccountID),
	)
}

func (e *estate) tofu(args ...string) error {
	c := exec.Command("tofu", args...)
	c.Dir = e.dir
	c.Env = e.backendEnv()
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("tofu %s: %w", args[0], err)
	}
	return nil
}

func (e *estate) tofuOutput(args ...string) (string, error) {
	c := exec.Command("tofu", args...)
	c.Dir = e.dir
	c.Env = e.backendEnv()
	var stderr bytes.Buffer
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		return "", fmt.Errorf("tofu %s: %w\n%s", args[0], err, stderr.String())
	}
	return string(out), nil
}

// sterilize removes what the run wrote that holds or points at a secret: the
// rendered config, and the backend record tofu init keeps beside the root.
func (e *estate) sterilize() {
	for _, path := range []string{e.rendered, filepath.Join(e.dir, ".terraform", "terraform.tfstate")} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			console.Warn(fmt.Sprintf("could not remove %s: %v", filepath.Base(path), err))
		}
	}
}
