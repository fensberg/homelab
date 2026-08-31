package repo

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ghcr.io/fensberg/homelab-runner is a PUBLIC package. Anything that reaches a
// layer of it is readable by anyone, forever, and container layers are
// append-only - a file added in one layer and deleted in a later one is still
// recoverable from the published image, and build arguments are readable with
// `docker history` whether or not the final image uses them.
//
// Deleting a secret afterwards is therefore not a remedy, and scanning the
// finished image is the weaker check because it can only find what it knows to
// look for. These close the routes in instead: nothing from the repository
// enters the build, and no secret is handed to it.
//
// This is the same reasoning as the Backup phase never writing plaintext state
// to disk - the property is that the secret was never there, not that it was
// removed.

var (
	copyOrAdd     = regexp.MustCompile(`(?mi)^\s*(COPY|ADD)\s`)
	secretishName = regexp.MustCompile(`(?i)(TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|PRIVATE|_KEY\b|APIKEY|API_KEY)`)
	argOrEnv      = regexp.MustCompile(`(?m)^\s*(?:ARG|ENV)\s+([A-Za-z_][A-Za-z0-9_]*)`)
)

func TestRunnerImageCannotReceiveASecret(t *testing.T) {
	root := repoRoot(t)
	dockerfile := readFile(t, filepath.Join(root, ".github", "runner-image", "Dockerfile"))

	// 1. Nothing from the repository is copied into the image. The build
	//    context is .github/runner-image/, but a COPY would still be able to
	//    reach whatever is placed there by an earlier workflow step.
	if m := copyOrAdd.FindString(dockerfile); m != "" {
		t.Errorf(`the runner image Dockerfile uses %s.

The image is published publicly. Nothing from the repository or the workspace
may enter a layer: a rendered config, a kubeconfig or an op-injected file would
become world-readable, and deleting it in a later layer does not remove it from
the published image. If a file genuinely must be baked in, that is an epoch-level
decision, not a line in a Dockerfile.`, strings.TrimSpace(m))
	}

	// 2. No build argument or environment variable is named like a secret.
	//    Build arguments are visible in `docker history` on a public image.
	for _, m := range argOrEnv.FindAllStringSubmatch(dockerfile, -1) {
		if secretishName.MatchString(m[1]) {
			t.Errorf(`the runner image Dockerfile declares %s, which is named like a secret.

Build arguments are readable with `+"`docker history`"+` on a published image, and
this one is public. Versions and architectures are the only things that belong
here.`, m[1])
		}
	}

	// 3. The workflow hands the build no secret. `docker login` legitimately
	//    uses one, so this looks only at the docker build invocation.
	workflow := readFile(t, filepath.Join(root, ".github", "workflows", "runner-image.yml"))
	build, ok := dockerBuildCommand(workflow)
	if !ok {
		t.Fatal("could not find the `docker build` invocation in runner-image.yml.\n\nThe publish step was restructured; re-check by hand that no secret is passed to the build, then update this test to the new shape.")
	}
	for _, bad := range []string{"secrets.", "--secret", "GITHUB_TOKEN", "$TOKEN"} {
		if strings.Contains(build, bad) {
			t.Errorf(`the docker build invocation in runner-image.yml references %q.

Nothing secret may be passed to this build - the image it produces is public,
and a build argument survives in the layer metadata. Credentials belong to the
registry login step, which is separate and may keep using them.`, bad)
		}
	}
}

// dockerBuildCommand returns the `docker build ...` invocation, following
// backslash continuations to the end of the command.
func dockerBuildCommand(workflow string) (string, bool) {
	lines := strings.Split(workflow, "\n")
	for i, line := range lines {
		if !strings.Contains(line, "docker build") {
			continue
		}
		var b strings.Builder
		for j := i; j < len(lines); j++ {
			b.WriteString(lines[j])
			b.WriteString("\n")
			if !strings.HasSuffix(strings.TrimSpace(lines[j]), `\`) {
				break
			}
		}
		return b.String(), true
	}
	return "", false
}

// A tag is a pointer somebody can move; the digest is the image. The scale set
// is what a converge actually runs in, so a mutable reference there means the
// estate can be changed by a registry push that never touched this repository -
// which is the one pin that silently is not one.
var scaleSetImage = regexp.MustCompile(`(?m)^\s*image:\s*(\S+)`)

func TestRunnerScaleSetPinsItsImageByDigest(t *testing.T) {
	path := filepath.Join(repoRoot(t), "clusters", "management", "infrastructure", "configs", "runner-scale-set.yaml")
	m := scaleSetImage.FindStringSubmatch(readFile(t, path))
	if m == nil {
		t.Fatal("runner-scale-set.yaml names no image.\n\nWithout one the scale set silently falls back to the chart's default runner, which carries none of the toolchain the program shells out to - a converge would fail at Render with \"1Password CLI not found on PATH\".")
	}
	if !strings.Contains(m[1], "@sha256:") {
		t.Errorf(`the runner scale set image %q is not pinned by digest.

A tag can be moved by anyone who can push to the registry, so the estate would
be converged by whatever that tag resolves to on the day - a change nothing in
this repository would record or review. Use image@sha256:...; :latest is for
humans reading the registry.`, m[1])
	}
}
