package config

import "path/filepath"

// A site is two OpenTofu roots, each a directory under management/ with its
// own state (docs/epochs/02-abstraction.md, "Inside a site, the split is two
// roots sharing one config").
//
// The cluster root builds the machines and ends at "the nodes are Ready". The
// platform root starts there: it puts on the cluster what Flux cannot put
// there itself, and its provider is configured from the cluster root's
// outputs rather than from a resource of its own.
//
// Named here, once, because the contractor, its declared steps, the backup
// layout and the test tiers all say which root they mean.
const (
	ClusterRoot  = "cluster"
	PlatformRoot = "platform"
)

// Roots is both, in the order they are applied.
var Roots = []string{ClusterRoot, PlatformRoot}

// ContractCorpus is where the config-contract corpus is, from the top of the
// repository: the fixtures both implementations of the config's rules are
// held to, and the OpenTofu test that runs them. It sits with the module that
// declares those rules, and everything that reads a fixture finds it here, so
// moving the module is this line.
const ContractCorpus = "modules/infrastructure/cluster/tests"

// CorpusFixture is the path of one fixture in that corpus.
func CorpusFixture(repoRoot, name string) string {
	return filepath.Join(repoRoot, filepath.FromSlash(ContractCorpus), "fixtures", name)
}
