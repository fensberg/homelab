// This application's own tests: what is true of it and of nothing else.
//
// They live here because they are the application's. Removing the application
// is deleting its directory, and a test of it kept among the repository's
// guards would be a file elsewhere that has to go too. The repository's
// guards run every application's tests (tests/go/repo, "an application's own
// tests"), handing each the application's manifests as JSON so that nothing
// here needs a YAML library: no dependency, no go.sum, nothing to keep
// current.
module homelab/applications/valheim/tests

go 1.26

require homelab/details v0.0.0

replace homelab/details => ../../../../scripts/details
