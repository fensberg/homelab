package repo

import (
	"regexp"
	"strings"
	"testing"

	"homelab/details/applications"
	"homelab/tests/parsed"
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
	secretishName = regexp.MustCompile(`(?i)(TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|PRIVATE|_KEY\b|APIKEY|API_KEY)`)
)

func TestRunnerImageCannotReceiveASecret(t *testing.T) {
	tree := repository(t)
	// Every image the estate orders for itself: the one a converge runs in
	// and any other a runner starts from. An application's image is its
	// application's to answer for.
	checked := 0
	for _, order := range tree.WorkOrders(t) {
		if order.Context == "" || strings.HasPrefix(order.Context, applications.Dir+"/") {
			continue
		}
		checked++
		rel := order.Context + "/Dockerfile"
		built := dockerfile(t, rel)

		// 1. Nothing from the repository is copied into the image. A COPY
		//    reaches whatever is in the build context, including what an
		//    earlier workflow step placed there. The one thing a COPY may
		//    take from is a stage this same file starts FROM a published
		//    image; a file of the repository's that a build needs is mounted
		//    for the step that reads it, and is in no layer.
		stages := map[string]bool{}
		for _, i := range built {
			if i.Cmd == "from" && len(i.Value) == 3 && strings.EqualFold(i.Value[1], "as") {
				stages[i.Value[2]] = true
			}
		}
		for _, i := range built {
			if i.Cmd != "copy" && i.Cmd != "add" {
				continue
			}
			if from := copiedFrom(i); i.Cmd == "copy" && stages[from] {
				continue
			}
			t.Errorf(`%s: %s

The image is published publicly. Nothing from the repository or the workspace
may enter a layer: a rendered config, a kubeconfig or an op-injected file would
become world-readable, and deleting it in a later layer does not remove it from
the published image. If a file genuinely must be baked in, that is an epoch-level
decision, not a line in a Dockerfile.`, rel, i)
		}

		// 2. No build argument or environment variable is named like a
		//    secret. Build arguments are visible in `docker history` on a
		//    public image.
		for _, name := range declaredNames(built) {
			if secretishName.MatchString(name) {
				t.Errorf(`%s declares %s, which is named like a secret.

Build arguments are readable with `+"`docker history`"+` on a published image, and
this one is public. Versions and architectures are the only things that belong
here.`, rel, name)
			}
		}
	}
	if checked == 0 {
		t.Fatal("the estate orders no image of its own, so nothing was checked")
	}

	// 3. The workflow hands the build no secret. `docker login` legitimately
	//    uses one, so this looks only at the docker build invocation.
	build, ok := dockerBuild(t)
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

// dockerBuild is the fabricator's `docker build ...` invocation, from the
// step's script as the workflow parses: the command, followed through its
// backslash continuations to its end.
func dockerBuild(t *testing.T) (string, bool) {
	t.Helper()
	for _, v := range parsed.Values(repository(t).YAMLDocuments(t, ".github/workflows/"+fabricatorWorkflow)) {
		if build, ok := dockerBuildCommand(v); ok {
			return build, true
		}
	}
	return "", false
}

// dockerBuildCommand returns the `docker build ...` invocation in a script,
// following backslash continuations to the end of the command.
func dockerBuildCommand(script string) (string, bool) {
	lines := strings.Split(script, "\n")
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

// copiedFrom is the stage a COPY takes from, or "" for the build context.
func copiedFrom(i dockerInstruction) string {
	for _, f := range i.Flags {
		if from, ok := strings.CutPrefix(f, "--from="); ok {
			return from
		}
	}
	return ""
}

// declaredNames is every build argument and environment variable a
// Dockerfile declares, by name.
func declaredNames(built []dockerInstruction) []string {
	var out []string
	for _, i := range built {
		switch i.Cmd {
		case "arg":
			for _, v := range i.Value {
				name, _, _ := strings.Cut(v, "=")
				out = append(out, name)
			}
		case "env":
			// Conftest gives each as its key, its value and "=".
			for k := 0; k+1 < len(i.Value); k += 3 {
				out = append(out, i.Value[k])
			}
		}
	}
	return out
}

// scaleSetImage is the image a scale set's manifest names. That every one is
// pinned by digest is a configuration rule (policy/conftest/runner_images.rego).
var scaleSetImage = regexp.MustCompile(`(?m)^\s*image:\s*(\S+)`)
