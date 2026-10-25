package repo

import (
	"homelab/tests/parsed"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The workstation and the runner have to agree on what tofu, rclone and the
// rest are. A plan produced by one version and applied by another is a
// difference nobody sees until it matters, and a self-hosted runner is exactly
// where that drift hides - the estate would be provisioned by one toolchain
// and converged by a different one, with nothing reporting a disagreement.
//
// So scripts/versions.env is the only place a version is written, and this
// asserts neither consumer restates one.
func TestVersionsAreDeclaredOnceAndSharedByBoth(t *testing.T) {
	t.Parallel()
	tree := repository(t)

	pins := versionsPinned(t)
	if len(pins) == 0 {
		t.Fatal("versions.env declares nothing, so this test proves nothing")
	}

	// The provisioning script must source the file rather than assign its own.
	script := parsed.Commands(tree.Shell(t, installDependencies))
	sources := slices.ContainsFunc(script, func(c parsed.Command) bool {
		return len(c.Words) > 1 && (c.Words[0] == "source" || c.Words[0] == ".") &&
			slices.ContainsFunc(c.Words[1:], func(w string) bool { return strings.Contains(w, "versions.env") })
	})
	if !sources {
		t.Error("install-dependencies.sh does not source scripts/versions.env; the workstation and the runner can now drift")
	}
	for k, v := range pins {
		if slices.ContainsFunc(script, func(c parsed.Command) bool { _, assigns := c.Assigns[k]; return assigns }) {
			t.Errorf("install-dependencies.sh assigns %s itself instead of taking it from versions.env (which says %s)", k, v)
		}
	}

	// The Dockerfile must take them as build arguments, not hard-code them.
	//
	// RUNNER_VERSION is here because it was not, and that is how the estate
	// stopped working: it sat as an ARG default in the Dockerfile, governed by
	// nothing, until GitHub retired that runner version server-side. Every pod
	// then registered, was refused by the broker, and exited, while the
	// converge job sat queued and no error anywhere named the cause.
	//
	// The mapping is identity for all four now, and the map is kept rather
	// than collapsed because it is the thing that would have to change if a
	// name ever diverged again.
	//
	// It did diverge once. rclone reads every flag from a matching
	// RCLONE_<FLAG> environment variable, and docker exposes build arguments
	// to the RUN that uses them, so an ARG called RCLONE_VERSION was read by
	// rclone as `--version=1.75.0` and every rclone invocation in the build
	// failed on it. The answer then was an alias here. That was half a fix:
	// .github/actions/versions exports the same pins into the job environment,
	// so the name went on breaking rclone everywhere outside the image build
	// until the Backup phase lost five nights to it (#487). The pin itself is
	// RCLONE_DEB_VERSION now, and the namespace is guarded in policy/conftest/rules/rclone_namespace/rclone_namespace.rego.
	argFor := map[string]string{
		"TOFU_VERSION":       "TOFU_VERSION",
		"RCLONE_DEB_VERSION": "RCLONE_DEB_VERSION",
		"RUNNER_VERSION":     "RUNNER_VERSION",
		"KUBECTL_VERSION":    "KUBECTL_VERSION",
	}

	args := map[string][]string{}
	for _, i := range dockerfile(t, runnerImageDockerfile) {
		if i.Cmd == "arg" {
			for _, v := range i.Value {
				name, value, _ := strings.Cut(v, "=")
				args[name] = append(args[name], value)
			}
		}
	}

	for envKey, arg := range argFor {
		if _, ok := pins[envKey]; !ok {
			t.Errorf("versions.env no longer declares %s, but the runner image still expects it", envKey)
			continue
		}
		if !slices.Contains(args[arg], "") {
			t.Errorf("the Dockerfile does not take %s as a bare build argument, so its value is not coming from versions.env", arg)
		}
		if slices.ContainsFunc(args[arg], func(v string) bool { return v != "" }) {
			t.Errorf("the Dockerfile gives %s a default, which is a second place a version is written", arg)
		}
		// The workflow half - that versions.env's value actually reaches the
		// build argument - is the fabricator's pin step, which passes every
		// bare ARG automatically. fabricator_test.go runs it against this
		// Dockerfile.
	}
}

// contractor shells out to these. A missing one fails at the phase that needs it,
// on the runner, minutes into a converge - which is how the image came to
// exist in the first place.
func TestRunnerImageCarriesEveryBinaryTheContractorInvokes(t *testing.T) {
	t.Parallel()
	installs := runs(dockerfile(t, runnerImageDockerfile))

	// Hand-maintained, and it drifted: the Health phase started shelling out
	// to talosctl in the same merge that added talosctl to the Dockerfile, and
	// this list - whose name promises EVERY binary - was not updated either
	// time. It happened to be harmless because the Dockerfile was right; a
	// list that has to be remembered will not stay right. Deriving it from the
	// source is filed separately.
	for _, bin := range contractorInvokes {
		// gh is deliberately absent: the contractor no longer invokes it, and
		// policy/semgrep/contractor-tools.yml keeps it that way. Adding it
		// here would be adding a supplier to fix a dependency that is gone.
		if !strings.Contains(installs, bin) {
			t.Errorf("the runner image does not install %q, which contractor shells out to", bin)
		}
	}

	// The image checks itself at build time. Without that, a missing binary is
	// found by a failing job rather than by a failing build.
	if !strings.Contains(installs, "command -v") {
		t.Error("the Dockerfile does not verify its own toolchain before publishing; a missing binary would surface as a failed job instead of a failed build")
	}
}

// runnerImageDockerfile is the estate runner's image.
var runnerImageDockerfile = filepath.ToSlash(filepath.Join(".github", "runner-image", "Dockerfile"))

// contractorInvokes is what the contractor shells out to: the vault's
// client, OpenTofu, the backups' key and copier, and the cluster's and the
// machines' clients. The converge's image carries every one; a pull
// request's carries only those its own jobs take
// (TestThePullRequestRunnerCarriesExactlyWhatItsJobsTake).
var contractorInvokes = []string{"op", "tofu", "age", "rclone", "kubectl", "talosctl"}
