package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"homelab/details/workorders"
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
	// Every image the estate orders for itself: the one a converge runs in
	// and any other a runner starts from. An application's image is its
	// application's to answer for.
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(workorders.Path)))
	if err != nil {
		t.Fatal(err)
	}
	own, err := workorders.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, order := range own {
		if order.Context == "" {
			continue
		}
		checked++
		rel := order.Context + "/Dockerfile"
		dockerfile := instructions(readFile(t, filepath.Join(root, filepath.FromSlash(rel))))

		// 1. Nothing from the repository is copied into the image. A COPY
		//    reaches whatever is in the build context, including what an
		//    earlier workflow step placed there. The one thing a COPY may
		//    take from is a stage this same file starts FROM a published
		//    image; a file of the repository's that a build needs is mounted
		//    for the step that reads it, and is in no layer.
		stages := map[string]bool{}
		for _, m := range regexp.MustCompile(`(?mi)^FROM\s+\S+\s+AS\s+(\S+)`).FindAllStringSubmatch(dockerfile, -1) {
			stages[m[1]] = true
		}
		for _, line := range strings.Split(dockerfile, "\n") {
			if !copyOrAdd.MatchString(line) {
				continue
			}
			if from := regexp.MustCompile(`^\s*COPY\s+(?:--\S+\s+)*--from=(\S+)`).FindStringSubmatch(line); from != nil && stages[from[1]] {
				continue
			}
			t.Errorf(`%s: %s

The image is published publicly. Nothing from the repository or the workspace
may enter a layer: a rendered config, a kubeconfig or an op-injected file would
become world-readable, and deleting it in a later layer does not remove it from
the published image. If a file genuinely must be baked in, that is an epoch-level
decision, not a line in a Dockerfile.`, rel, strings.TrimSpace(line))
		}

		// 2. No build argument or environment variable is named like a
		//    secret. Build arguments are visible in `docker history` on a
		//    public image.
		for _, m := range argOrEnv.FindAllStringSubmatch(dockerfile, -1) {
			if secretishName.MatchString(m[1]) {
				t.Errorf(`%s declares %s, which is named like a secret.

Build arguments are readable with `+"`docker history`"+` on a published image, and
this one is public. Versions and architectures are the only things that belong
here.`, rel, m[1])
			}
		}
	}
	if checked == 0 {
		t.Fatal("the estate orders no image of its own, so nothing was checked")
	}

	// 3. The workflow hands the build no secret. `docker login` legitimately
	//    uses one, so this looks only at the docker build invocation.
	workflow := workflowText(t, fabricatorWorkflow)
	build, ok := dockerBuildCommand(workflow)
	if !ok {
		t.Fatal("could not find the `docker build` invocation in fabricator.yml.\n\nThe publish step was restructured; re-check by hand that no secret is passed to the build, then update this test to the new shape.")
	}
	for _, bad := range []string{"secrets.", "--secret", "GITHUB_TOKEN", "$TOKEN"} {
		if strings.Contains(build, bad) {
			t.Errorf(`the docker build invocation in fabricator.yml references %q.

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
	// Every set of runners, not the one this was written for.
	for _, release := range runnerSets(t) {
		t.Run(release, func(t *testing.T) { runnerImageIsPinned(t, release) })
	}
}

// runnerImageIsPinned is that rule for one set of runners, by its release.
func runnerImageIsPinned(t *testing.T, release string) {
	_, manifest := fluxObject(t, kindHelmRelease, release)
	m := scaleSetImage.FindStringSubmatch(manifest)
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
