# An App token's inputs are fed the value their names say: client-id the
# App's Client ID, app-id its numeric id. One fed the other failed every
# mint for a week with "Integration not found" (#439).
package rules.app_token_inputs

import data.lib
import rego.v1

token_inputs contains [file, field, secret] if {
	some file, doc in lib.workflows
	walk(doc, [where, v])
	count(where) > 0
	field := where[count(where) - 1]
	field in {"client-id", "app-id"}
	is_string(v)
	m := regex.find_all_string_submatch_n(`^\s*\$\{\{\s*(?:secrets|vars)\.([A-Za-z0-9_]+)\s*\}\}`, v, 1)[0]
	secret := m[1]
}

wanted := {"client-id": "_CLIENT_ID", "app-id": "_APP_ID"}

deny contains msg if {
	some [file, field, secret] in token_inputs
	not endswith(secret, wanted[field])
	msg := $"{file} passes {secret} to `{field}`. That input takes a value the name should say: client-id is the App's Client ID (beginning \"Iv\"), and app-id is the numeric App ID. A secret named for one and fed to the other is how the expedite duty failed every token mint for a week with \"Integration not found\" (#439). Name it *{wanted[field]}, and store the value that name says."
}

deny contains msg if {
	count(token_inputs) == 0
	msg := "no workflow passes a secret to a GitHub App token input, so the token-input rule checked nothing"
}
