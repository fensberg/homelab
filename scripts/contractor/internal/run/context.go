// Package run holds the state and helpers every phase shares: paths, the
// selected site, process flags, coloured phase output, and thin wrappers
// around the external commands (tofu, ping, TCP dials) this program only
// ever orchestrates rather than reimplements.
package run

import "path/filepath"

// Context is built once in main and passed to every phase by reference.
type Context struct {
	RepoRoot          string
	ConfigTpl         string
	ConfigRendered    string
	HypervisorDir     string
	InventoryOut      string
	OverlayVars       string
	SiteVars          string
	ClusterDir        string
	TofuBackendRecord string

	// The saved plan. It holds every attribute of every resource it touches,
	// so it is sterilized like a secret rather than treated as a build artifact.
	TofuPlanFile string

	// CommentOut, when set, is where the plan writes the pull request comment
	// body. Empty means write nothing, which is every case but CI.
	CommentOut   string
	BackendPgOff string
	BackendPgOn  string
	LocalState   string

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

	// Converge means the estate already exists: attach to its state rather
	// than build from scratch, and never destroy on failure.
	Converge bool

	// PreexistingEstate means this run did not create what it is looking at,
	// so it must never tear it down. True for every verb except ignite.
	//
	// Kept separate from Converge because the property is broader than one
	// verb: a plan creates nothing at all and still reached the destroy path,
	// which is how a read-only command became able to delete an estate.
	PreexistingEstate bool
}

func NewContext(repoRoot, site string) *Context {
	hypervisorDir := filepath.Join(repoRoot, "management", "hypervisor")
	clusterDir := filepath.Join(repoRoot, "management", "cluster")
	return &Context{
		RepoRoot:          repoRoot,
		ConfigTpl:         filepath.Join(repoRoot, "config", "management.tpl.json"),
		ConfigRendered:    filepath.Join(repoRoot, "config", "management.rendered.json"),
		HypervisorDir:     hypervisorDir,
		InventoryOut:      filepath.Join(hypervisorDir, "inventory.yml"),
		OverlayVars:       filepath.Join(hypervisorDir, "overlay-network.auto.yml"),
		SiteVars:          filepath.Join(hypervisorDir, "site.auto.yml"),
		ClusterDir:        clusterDir,
		TofuBackendRecord: filepath.Join(clusterDir, ".terraform", "terraform.tfstate"),
		TofuPlanFile:      filepath.Join(clusterDir, "tfplan"),
		BackendPgOff:      filepath.Join(clusterDir, "backend_pg.tf.disabled"),
		BackendPgOn:       filepath.Join(clusterDir, "backend_pg.tf"),
		LocalState:        filepath.Join(clusterDir, "terraform.tfstate"),
		Kubeconfig:        filepath.Join(clusterDir, "kubeconfig"),
		Site:              site,
	}
}
