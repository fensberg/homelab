package config

// EnrollmentAppAddress is the device-enrollment Access application, in state.
//
// It is the estate's, not the site's. The Cloudflare account - and the Zero
// Trust organisation inside it, which creates this application and allows
// exactly one - is the estate, so no site's teardown may destroy it: the
// Cluster phase adopts it when it exists, and a site teardown forgets it rather
// than destroying it. Destroying it is what a demolish did on
// 2026-09-25 - it deleted the organisation's only enrollment application, and
// the next ignition, built to adopt the one that exists, found none.
//
// Declared here because the phase that adopts it and the teardown that
// forgets it must name the same address.
const EnrollmentAppAddress = "cloudflare_zero_trust_access_application.enrollment"

// AccessAppsAPIURL is where the vendor's API lists an account's Access
// applications - the question "does this organisation have its enrollment
// application", asked before OpenTofu is.
func AccessAppsAPIURL(accountID string) string {
	return "https://api.cloudflare.com/client/v4/accounts/" + accountID + "/access/apps"
}
