// Package flux names the kinds of Flux object this estate's programs and
// guards tell apart.
//
// The contractor's health gate asks which Kustomizations and HelmReleases
// have reconciled; the guards over a site's applications tell a release
// source from the Kustomization that runs it. Each reads a kind out of a
// document and compares it, so the kinds are written here once.
package flux

const (
	// Kustomization reconciles a directory of manifests from a source.
	Kustomization = "Kustomization"
	// HelmRelease reconciles a chart.
	HelmRelease = "HelmRelease"
	// OCIRepository is a source pinned to an artifact in a registry: how a
	// site names the release of an application it runs.
	OCIRepository = "OCIRepository"
)
