// Package run holds the state and helpers every phase shares: paths, the
// selected site, process flags, coloured phase output, and thin wrappers
// around the external commands (tofu, ping, TCP dials) this program only
// ever orchestrates rather than reimplements.
package run

import (
	"path/filepath"

	"homelab/contractor/config"
)

// KeepOnFailureFlag is the flag that sets KeepOnFailure, named once so the
// advice that tells an operator to use it cannot name a flag that is not there.
const KeepOnFailureFlag = "keep-on-failure"

// Root is one OpenTofu root of a site, and the files a run leaves in it.
//
// A site has two (docs/epochs/02-abstraction.md, "Inside a site, the split is
// two roots sharing one config"): the cluster root builds the machines and
// ends at "the nodes are Ready", and the platform root puts on that cluster
// what Flux cannot put there itself. Each has its own state.
type Root struct {
	// Name is one of config.Roots: what a log calls it, and its directory
	// under management/.
	Name string
	Dir  string

	BackendRecord string

	// The saved plan. It holds every attribute of every resource it touches,
	// so it is sterilized like a secret rather than treated as a build artifact.
	PlanFile string

	BackendPgOff string
	BackendPgOn  string
	LocalState   string
}

func newRoot(repoRoot, name string) Root {
	dir := filepath.Join(repoRoot, "management", name)
	return Root{
		Name:          name,
		Dir:           dir,
		BackendRecord: filepath.Join(dir, ".terraform", "terraform.tfstate"),
		PlanFile:      filepath.Join(dir, "tfplan"),
		BackendPgOff:  filepath.Join(dir, "backend_pg.tf.disabled"),
		BackendPgOn:   filepath.Join(dir, "backend_pg.tf"),
		LocalState:    filepath.Join(dir, "terraform.tfstate"),
	}
}

// Context is built once in main and passed to every phase by reference.
type Context struct {
	RepoRoot       string
	ConfigTpl      string
	ConfigRendered string
	HypervisorDir  string
	InventoryOut   string
	OverlayVars    string
	SiteVars       string

	// Root is the root tofu runs in: the cluster's, unless a phase asked for
	// the platform's with In. Cluster and Platform are the two, always.
	Root
	Cluster  Root
	Platform Root

	// AsBuiltDir is where the Record phase works: an offline copy of the
	// cluster root and a throwaway CA. Two levels below the repository, the
	// same depth as the root it copies, so "${path.module}/../../" still
	// reaches the repository.
	AsBuiltDir string

	// RecordOut, when set, is where the Record phase saves a publishable
	// record for a later plan to read. Empty means take it, report it, and
	// keep nothing.
	RecordOut string

	// CommentOut, when set, is where the plan writes the pull request comment
	// body. Empty means write nothing, which is every case but CI.
	CommentOut string

	// Written only by `task kubeconfig`, for a human who wants to look at the
	// cluster. Nothing in the ignition sequence creates it - the Health phase
	// uses a temporary file it removes itself - but Sterilize owns it, because
	// a kubeconfig is a credential and this one persists on purpose.
	Kubeconfig string

	Site          string
	Upgrade       bool
	SkipOverlay   bool
	SkipUpgrade   bool
	DryRun        bool
	KeepOnFailure bool

	// Converge means the estate already exists: take over its state rather
	// than build from scratch, and never destroy on failure.
	Converge bool

	// StateSerialAtTakeover is the state's serial number as TakeOver found it,
	// and TakenOverOK says whether it was ever read. Together they are how a
	// failed run answers "did anything actually change" with a measurement
	// instead of a promise. Nothing before TakeOver can run tofu - the building
	// code in tests/go/repo enforces it - so an unset TakenOverOK proves the
	// estate is untouched rather than merely suggesting it.
	StateSerialAtTakeover int64
	TakenOverOK           bool

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
	cluster := newRoot(repoRoot, config.ClusterRoot)
	return &Context{
		Root:           cluster,
		Cluster:        cluster,
		Platform:       newRoot(repoRoot, config.PlatformRoot),
		RepoRoot:       repoRoot,
		ConfigTpl:      filepath.Join(repoRoot, "config", "management.tpl.json"),
		ConfigRendered: filepath.Join(repoRoot, "config", "management.rendered.json"),
		HypervisorDir:  hypervisorDir,
		InventoryOut:   filepath.Join(hypervisorDir, "inventory.yml"),
		OverlayVars:    filepath.Join(hypervisorDir, "overlay-network.auto.yml"),
		SiteVars:       filepath.Join(hypervisorDir, "site.auto.yml"),
		AsBuiltDir:     filepath.Join(repoRoot, ".as-built"),
		Kubeconfig:     filepath.Join(cluster.Dir, "kubeconfig"),
		Site:           site,
	}
}

// Roots is the site's roots in the order they are applied.
func (c *Context) Roots() []Root { return []Root{c.Cluster, c.Platform} }

// In is this context with tofu running in r. A copy: what a phase records
// about the run (the state serial at take-over) it records on the context it
// was given, never on one of these.
func (c *Context) In(r Root) *Context {
	cp := *c
	cp.Root = r
	return &cp
}
