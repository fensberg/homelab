// Package run holds the state and helpers every phase shares: paths, the
// selected site, process flags, coloured phase output, and thin wrappers
// around the external commands (tofu, ping, TCP dials) this program only
// ever orchestrates rather than reimplements.
package run

import "path/filepath"

// Context is built once in main and passed to every phase by reference.
type Context struct {
	RepoRoot       string
	ConfigTpl      string
	ConfigRendered string
	HypervisorDir  string
	InventoryOut   string
	OverlayVars    string
	SiteVars       string
	ClusterDir     string
	BackendPgOff   string
	BackendPgOn    string
	LocalState     string

	// Written only by `task kubeconfig`, for a human who wants to look at the
	// cluster. Nothing in the ignition sequence creates it - the Health phase
	// uses a temporary file it removes itself - but Sterilize owns it, because
	// a kubeconfig is a credential and this one persists on purpose.
	Kubeconfig string

	Site          string
	Upgrade       bool
	SkipOverlay   bool
	SkipUpgrade   bool
	KeepOnFailure bool
}

func NewContext(repoRoot, site string) *Context {
	hypervisorDir := filepath.Join(repoRoot, "management", "hypervisor")
	clusterDir := filepath.Join(repoRoot, "management", "cluster")
	return &Context{
		RepoRoot:       repoRoot,
		ConfigTpl:      filepath.Join(repoRoot, "config", "management.tpl.json"),
		ConfigRendered: filepath.Join(repoRoot, "config", "management.rendered.json"),
		HypervisorDir:  hypervisorDir,
		InventoryOut:   filepath.Join(hypervisorDir, "inventory.yml"),
		OverlayVars:    filepath.Join(hypervisorDir, "overlay-network.auto.yml"),
		SiteVars:       filepath.Join(hypervisorDir, "site.auto.yml"),
		ClusterDir:     clusterDir,
		BackendPgOff:   filepath.Join(clusterDir, "backend_pg.tf.disabled"),
		BackendPgOn:    filepath.Join(clusterDir, "backend_pg.tf"),
		LocalState:     filepath.Join(clusterDir, "terraform.tfstate"),
		Kubeconfig:     filepath.Join(clusterDir, "kubeconfig"),
		Site:           site,
	}
}
