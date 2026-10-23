package repo

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hclsyntax"

	"homelab/details/ghapi"
	"homelab/tests/parsed"
)

// The estate's objects are declared in the estate's root and nowhere else.
//
// Things are built in three scopes. The estate - the Cloudflare account and its
// Zero Trust organisation - belongs to every site; a site belongs to every node
// in it; and an operation at one scope must be unable to harm anything wider.
// The enrollment application once lived in a site's state, adopted there, and
// a site demolish destroyed it for the whole account; the next site build then
// failed looking for it (#531).
//
// The boundary is state, not care: management/estate/ is its own OpenTofu root
// with its own state, so a site's plan has no handle on an estate object and a
// site destroy cannot reach one. That holds only while nobody declares an
// estate object in a site root again, which is what this refuses. It walks
// every .tf file in the repository rather than a list of roots, so a new root
// is covered the moment it exists.
//
// And the reverse, with unknown meaning fail: every resource in the estate root
// must be a type listed here as the estate's. A new estate object is a
// decision about scope, and this is where that decision is written down.
var estateTypes = map[string]bool{
	// Who may enroll a device, and what an enrolled device reaches.
	"cloudflare_zero_trust_access_policy":          true,
	"cloudflare_zero_trust_access_application":     true,
	"cloudflare_zero_trust_device_default_profile": true,
	// Each site's plot, created by the estate because only an account-wide
	// token can create it, and granted to the site as narrowed credentials.
	"cloudflare_r2_bucket":                            true,
	"cloudflare_account_token":                        true,
	"cloudflare_zero_trust_tunnel_cloudflared":        true,
	"cloudflare_zero_trust_tunnel_cloudflared_config": true,
	"cloudflare_zero_trust_tunnel_cloudflared_route":  true,
	// The rule that answers an enrolled device, and nobody else, with where
	// a name a site serves is reached.
	"cloudflare_zero_trust_gateway_policy": true,
	// Whether the vendor carries what a device sends to a private name at
	// all: one setting for the account.
	"cloudflare_zero_trust_device_settings": true,
	// The watchman, which says when a site has gone quiet. The estate's
	// because it has to outlive whatever it watches: one in a site's state
	// is destroyed with the site.
	"cloudflare_workers_script":           true,
	"cloudflare_workers_script_subdomain": true,
	"cloudflare_workers_cron_trigger":     true,
	"cloudflare_workers_kv_namespace":     true,
	// What each site rings the watchman with.
	"random_password": true,
	// What a workflow in a repository of the estate's may run, and what the
	// token it is handed may do: the estate is every repository under its
	// organisation, and a site has none.
	"github_actions_repository_permissions":  true,
	"github_workflow_repository_permissions": true,
	// What may be pushed to a repository's branches and tags.
	ghapi.RulesetType: true,
	// What a member of the organisation may do for being one.
	ghapi.OrganisationSettingsType: true,
	// Which repositories and workflows may use the estate's own runners.
	ghapi.RunnerGroupType: true,
	// What a workflow reads that is the estate's to say: which site a pull
	// request's jobs are sent to.
	"github_actions_variable": true,
	// Where a job is handed the estate's own credential, and on which
	// branch it may be.
	"github_repository_environment":                   true,
	"github_repository_environment_deployment_policy": true,
}

const estateRoot = "management/estate"

// owned is each type a file's blocks own an object of. A `resource` block
// owns an object, and an `import` block takes one into state; both put it
// where a destroy reaches it. A `data` block only reads, which is how a site
// is meant to see the estate.
func owned(f parsed.OpenTofuFile) []string {
	var out []string
	for _, b := range f.Body.Blocks {
		switch b.Type {
		case "resource":
			out = append(out, b.Labels[0])
		case "import":
			if to, ok := b.Body.Attributes["to"]; ok {
				if ref, ok := to.Expr.(*hclsyntax.ScopeTraversalExpr); ok {
					out = append(out, ref.Traversal.RootName())
				}
			}
		}
	}
	return out
}

func TestEstateObjectsAreDeclaredOnlyInTheEstateRoot(t *testing.T) {
	estateDeclares := 0
	for _, f := range repository(t).OpenTofu(t) {
		rel := f.Path
		// The estate root and the modules beneath it: a site's plot is
		// declared once in management/estate/site and instantiated per site.
		inEstate := filepath.Dir(rel) == estateRoot || strings.HasPrefix(filepath.Dir(rel), estateRoot+"/")
		for _, typ := range owned(f) {
			switch {
			case inEstate && !estateTypes[typ]:
				t.Errorf("%s declares a %s, which is not listed as the estate's.\n\n"+
					"Everything in %s belongs to every site in the account. If this object "+
					"does, add its type to estateTypes in this test; if it belongs to one "+
					"site, it goes in that site's root instead.", rel, typ, estateRoot)
			case inEstate:
				estateDeclares++
			case estateTypes[typ]:
				t.Errorf("%s declares a %s, which is the estate's.\n\n"+
					"An estate object in a site's state is destroyed by that site's demolish, "+
					"for every site in the account - which is how the enrollment application "+
					"was lost (#531). Declare it in %s; a site that needs it reads it with a "+
					"data source.", rel, typ, estateRoot)
			}
		}
	}
	if estateDeclares == 0 {
		t.Fatalf("found no resource in %s, so this checked nothing - the root has moved", estateRoot)
	}
}
