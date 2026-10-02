package phases

import (
	"fmt"
	"os"
	"strings"

	"homelab/contractor/internal/run"
	"homelab/details/platform"
	"homelab/details/repopath"
)

// NameRelease tells the site's roots which release of the platform to run:
// the one the site's line in management/versions.json names, fetched by its
// digest from this repository's registry (homelab/details/platform). Every
// verb does this before anything else, so whichever of them runs tofu, a
// site runs the version it was given and never the checkout.
//
// The roots fetch the release themselves, at init. This says where and
// which; the credential the registry wants is the Render phase's to write
// (fetchCredential), and one an earlier phase of the same run wrote is
// picked up here.
func NameRelease(ctx *run.Context) error {
	release, err := platform.Pinned(ctx.RepoRoot, ctx.Site)
	if err != nil {
		return err
	}
	repository, err := repopath.Slug()
	if err != nil {
		return fmt.Errorf("finding which repository's releases to fetch: %w", err)
	}
	named := map[string]string{
		platform.ReleaseVariable: platform.Registry(repository),
		platform.DigestVariable:  release.Digest,
	}
	if _, err := os.Stat(ctx.RegistryCredential); err == nil {
		named[platform.CLIConfigVariable] = ctx.RegistryCredential
	}
	for name, value := range named {
		if err := os.Setenv(name, value); err != nil {
			return err
		}
	}
	// A checkout's own modules are for a check. Whatever this was started
	// with, a run against an estate does not read them.
	return os.Unsetenv(platform.UnreleasedVariable)
}

// fetchCredential writes the settings tofu fetches the release with: a
// credential the registry will take, which it wants even for a public
// package. One of the things a run renders, and removed with them.
func fetchCredential(ctx *run.Context) error {
	secret, err := registryToken()
	if err != nil {
		return err
	}
	settings, err := platform.CLIConfig(secret)
	if err != nil {
		return err
	}
	release, err := platform.Pinned(ctx.RepoRoot, ctx.Site)
	if err != nil {
		return err
	}
	if err := os.WriteFile(ctx.RegistryCredential, settings, 0o600); err != nil {
		return err
	}
	if err := os.Setenv(platform.CLIConfigVariable, ctx.RegistryCredential); err != nil {
		return err
	}
	run.Ok("the site runs platform " + release.Version)
	return nil
}

// forgetCredential stops pointing tofu at those settings once they are gone:
// whatever removes what a run rendered calls this after. A tofu told of
// settings that are not there says so on every command, into output this
// program reads.
func forgetCredential(ctx *run.Context) error {
	if _, err := os.Stat(ctx.RegistryCredential); err == nil {
		return nil
	}
	if os.Getenv(platform.CLIConfigVariable) == ctx.RegistryCredential {
		return os.Unsetenv(platform.CLIConfigVariable)
	}
	return nil
}

// registryToken is a token the registry will take, from the environment: the
// one a workflow hands the step, or the one `task` reads from this
// workstation's GitHub sign-in before it runs this. The package is public,
// so which token does not matter, only that there is one. Read and never
// asked for: this program runs where the GitHub client is not installed.
func registryToken() (string, error) {
	for _, name := range tokenVariables {
		if token := strings.TrimSpace(os.Getenv(name)); token != "" {
			return token, nil
		}
	}
	return "", fmt.Errorf("no GitHub token to fetch the platform's release with: %s is not set. In a workflow, hand the step the job's token. At a workstation, run this through task, which reads the one your GitHub sign-in holds, or export it yourself", tokenVariables[0])
}

// tokenVariables is where a GitHub token is looked for, in order.
var tokenVariables = []string{"GH_TOKEN", "GITHUB_TOKEN"}
