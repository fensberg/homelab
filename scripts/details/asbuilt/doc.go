// Package asbuilt turns a site's real state into its as-built record: the
// estate as last converged, with every value worth stealing replaced, so that
// a plan can run against it with no credential and no network (#554).
//
// A construction site's as-built drawings record what was actually built,
// which is what a pull request's plan needs to compare against. These ones
// are safe to hand to anybody, because nothing they hold is real:
//
//   - every vault value is a keyed fingerprint, shaped where the code needs a
//     shape, and consistent everywhere it appears - including in resource
//     addresses, where a for_each key can be a vault value;
//   - Talos's CA and machine secrets are a throwaway set generated for this
//     record, so no certificate the cluster trusts is in it;
//   - every other value the state marks sensitive is replaced the same way;
//   - what the offline plan then disagrees with is noise from those
//     replacements, and is folded back until the plan is quiet.
//
// The key is random per record and never stored. A fingerprint only has to
// agree with itself inside one record, and a key that no longer exists cannot
// be used to test guesses against the record afterwards.
//
// Everything here is pure: tofu is run by the Record phase, which hands this
// package JSON. That split is what lets the properties be tested against
// fabricated state without an estate.
package asbuilt
