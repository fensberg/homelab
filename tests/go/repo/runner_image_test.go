package repo

import (
	"os"
	"path/filepath"
	"regexp"
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
	root := repoRoot(t)

	env := readFile(t, filepath.Join(root, "scripts", "versions.env"))
	pins := map[string]string{}
	for _, line := range strings.Split(env, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("versions.env line is not KEY=VALUE: %q", line)
		}
		pins[k] = v
	}
	if len(pins) == 0 {
		t.Fatal("versions.env declares nothing, so this test proves nothing")
	}

	// The provisioning script must source the file rather than assign its own.
	script := readFile(t, filepath.Join(root, "scripts", "install-dependencies.sh"))
	if !strings.Contains(script, "versions.env") {
		t.Error("install-dependencies.sh does not source scripts/versions.env; the workstation and the runner can now drift")
	}
	for k, v := range pins {
		if regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(k) + `=`).MatchString(script) {
			t.Errorf("install-dependencies.sh assigns %s itself instead of taking it from versions.env (which says %s)", k, v)
		}
	}

	// The Dockerfile must take them as build arguments, not hard-code them,
	// and the workflow must actually wire versions.env's value into each one.
	//
	// RUNNER_VERSION is here because it was not, and that is how the estate
	// stopped working: it sat as an ARG default in the Dockerfile, governed by
	// nothing, until GitHub retired that runner version server-side. Every pod
	// then registered, was refused by the broker, and exited, while the
	// converge job sat queued and no error anywhere named the cause.
	//
	// The two names are not always identical. rclone reads every flag from a
	// matching RCLONE_<FLAG> environment variable, and docker exposes build
	// arguments to the RUN that uses them, so an ARG called RCLONE_VERSION is
	// read by rclone as `--version=1.75.0` and every rclone invocation in the
	// build fails on it. Hence the alias - and hence the workflow check below,
	// because an alias that nothing passes a value to is a version silently
	// defaulting to empty rather than coming from versions.env.
	argFor := map[string]string{
		"TOFU_VERSION":    "TOFU_VERSION",
		"RCLONE_VERSION":  "RCLONE_DEB_VERSION",
		"RUNNER_VERSION":  "RUNNER_VERSION",
		"KUBECTL_VERSION": "KUBECTL_VERSION",
	}

	dockerfile := readFile(t, filepath.Join(root, ".github", "runner-image", "Dockerfile"))
	workflow := readFile(t, filepath.Join(root, ".github", "workflows", "runner-image.yml"))

	for envKey, arg := range argFor {
		if _, ok := pins[envKey]; !ok {
			t.Errorf("versions.env no longer declares %s, but the runner image still expects it", envKey)
			continue
		}
		if !regexp.MustCompile(`(?m)^ARG\s+` + arg + `\s*$`).MatchString(dockerfile) {
			t.Errorf("the Dockerfile does not take %s as a bare build argument, so its value is not coming from versions.env", arg)
		}
		if regexp.MustCompile(`(?m)^ARG\s+` + arg + `\s*=`).MatchString(dockerfile) {
			t.Errorf("the Dockerfile gives %s a default, which is a second place a version is written", arg)
		}
		// --build-arg <ARG>="$<ENVKEY>" - the link that makes the alias safe.
		wired := regexp.MustCompile(`--build-arg\s+` + arg + `="\$` + envKey + `"`)
		if !wired.MatchString(workflow) {
			t.Errorf("runner-image.yml does not pass versions.env's %s into the Dockerfile's %s build argument;\nthe image would build with an empty %s instead of the pinned version", envKey, arg, arg)
		}
	}
}

// versions.env is only "the one place a version is pinned" if nothing else
// writes one down. Workflows were the leak: pr-validation.yml carried its own
// TOFU_VERSION for long enough that CI validated pull requests on OpenTofu
// 1.10.6 while the estate was converged with 1.12.6 - two minor versions
// apart, agreeing with nothing, reported by nobody.
//
// This only rejects an *assignment* of a literal. Referring to a pinned
// version is the entire point (`--build-arg TOFU_VERSION="$TOFU_VERSION"`,
// `${{ env.GO_VERSION }}`), so a value that starts with $ is a reference and
// passes.
func TestNoWorkflowDeclaresAVersionThatVersionsEnvOwns(t *testing.T) {
	root := repoRoot(t)
	pins := pinnedKeys(t, root)

	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	// The action every workflow reads them through. If it disappears, the
	// workflows have no way to get a version and this test would pass by
	// checking nothing.
	action := filepath.Join(root, ".github", "actions", "versions", "action.yml")
	if _, err := os.Stat(action); err != nil {
		t.Fatalf(".github/actions/versions/action.yml is missing: %v\n\nWithout it a workflow cannot read scripts/versions.env, and the only way to give a job a version is to declare one - which is what this test forbids.", err)
	}

	var checked int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		checked++
		body := readFile(t, filepath.Join(dir, e.Name()))
		for _, key := range pins {
			// `KEY: literal` (YAML) or `KEY=literal` (shell), where the
			// value is not a $reference.
			assign := regexp.MustCompile(`(?m)^\s*` + key + `\s*[:=]\s*["']?[^$\s"']`)
			if loc := assign.FindStringIndex(body); loc != nil {
				line := 1 + strings.Count(body[:loc[0]], "\n")
				t.Errorf(".github/workflows/%s:%d declares %s itself.\n\nversions.env owns that pin. Read it with `uses: ./.github/actions/versions` and refer to ${{ env.%s }} instead - a second declaration is a version that drifts silently.", e.Name(), line, key, key)
			}
		}
	}
	if checked == 0 {
		t.Fatal("scanned no workflows, so this test proves nothing")
	}
}

// The composite action only exports a line whose key ends in _VERSION or
// _SHA256 and whose value starts alphanumerically and holds nothing but
// [A-Za-z0-9._+-]. That pattern is a security boundary, not a convenience:
// this repository is public and pr-validation.yml runs on pull_request, so on
// a fork's branch versions.env is attacker-controlled, and a free-form
// KEY=VALUE written to $GITHUB_ENV is how LD_PRELOAD or NODE_OPTIONS becomes
// code execution in a later step.
//
// The cost of a narrow pattern is that a key outside it is skipped in
// silence, and the job then runs with an empty version rather than failing.
// So every key here must be one the action can actually export.
var exportable = regexp.MustCompile(`^[A-Z][A-Z0-9_]*_(VERSION|SHA256)=[A-Za-z0-9][A-Za-z0-9._+-]*$`)

func TestEveryPinnedVersionCanActuallyBeExported(t *testing.T) {
	root := repoRoot(t)
	for _, line := range strings.Split(readFile(t, filepath.Join(root, "scripts", "versions.env")), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !exportable.MatchString(line) {
			key, _, _ := strings.Cut(line, "=")
			t.Errorf("versions.env declares %q, which .github/actions/versions will not export.\n\nA key must end in _VERSION or _SHA256, and a value must begin with a letter or digit and contain only letters, digits, dot, underscore, plus or dash. Anything else is skipped silently and the job runs with an empty value - see the action for why the pattern is deliberately this narrow.", key)
		}
	}

	// And the action must still be the thing enforcing it. If the pattern is
	// loosened there, this test would keep passing while the boundary is gone.
	action := readFile(t, filepath.Join(root, ".github", "actions", "versions", "action.yml"))
	if !strings.Contains(action, "_(VERSION|SHA256)=[A-Za-z0-9][A-Za-z0-9._+-]*$") {
		t.Error(".github/actions/versions no longer restricts what it writes to $GITHUB_ENV.\n\nOn a fork's pull request versions.env is attacker-controlled, and an unconstrained write to the environment file is code execution in every later step - including the TruffleHog lane, which is the one lane deliberately not egress-blocked.")
	}
}

// pinnedKeys returns every key versions.env declares.
func pinnedKeys(t *testing.T, root string) []string {
	t.Helper()
	var keys []string
	for _, line := range strings.Split(readFile(t, filepath.Join(root, "scripts", "versions.env")), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, _, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("versions.env line is not KEY=VALUE: %q", line)
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		t.Fatal("versions.env declares nothing, so this test proves nothing")
	}
	return keys
}

// contractor shells out to these. A missing one fails at the phase that needs it,
// on the runner, minutes into a converge - which is how the image came to
// exist in the first place.
func TestRunnerImageCarriesEveryBinaryTheContractorInvokes(t *testing.T) {
	dockerfile := readFile(t, filepath.Join(repoRoot(t), ".github", "runner-image", "Dockerfile"))

	// Hand-maintained, and it drifted: the Health phase started shelling out
	// to talosctl in the same merge that added talosctl to the Dockerfile, and
	// this list - whose name promises EVERY binary - was not updated either
	// time. It happened to be harmless because the Dockerfile was right; a
	// list that has to be remembered will not stay right. Deriving it from the
	// source is filed separately.
	for _, bin := range []string{"op", "tofu", "age", "rclone", "kubectl", "talosctl"} {
		// gh is deliberately absent: the contractor no longer invokes it, and
		// tests/go/repo/no_gh_dependency_test.go keeps it that way. Adding it
		// here would be adding a supplier to fix a dependency that is gone.
		if !strings.Contains(dockerfile, bin) {
			t.Errorf("the runner image does not install %q, which contractor shells out to", bin)
		}
	}

	// The image checks itself at build time. Without that, a missing binary is
	// found by a failing job rather than by a failing build.
	if !strings.Contains(dockerfile, "command -v") {
		t.Error("the Dockerfile does not verify its own toolchain before publishing; a missing binary would surface as a failed job instead of a failed build")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}
