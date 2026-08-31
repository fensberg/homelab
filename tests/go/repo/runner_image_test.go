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
	// The two names are not always identical. rclone reads every flag from a
	// matching RCLONE_<FLAG> environment variable, and docker exposes build
	// arguments to the RUN that uses them, so an ARG called RCLONE_VERSION is
	// read by rclone as `--version=1.75.0` and every rclone invocation in the
	// build fails on it. Hence the alias - and hence the workflow check below,
	// because an alias that nothing passes a value to is a version silently
	// defaulting to empty rather than coming from versions.env.
	argFor := map[string]string{
		"TOFU_VERSION":   "TOFU_VERSION",
		"RCLONE_VERSION": "RCLONE_DEB_VERSION",
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

// steward shells out to these. A missing one fails at the phase that needs it,
// on the runner, minutes into a converge - which is how the image came to
// exist in the first place.
func TestRunnerImageCarriesEveryBinaryStewardInvokes(t *testing.T) {
	dockerfile := readFile(t, filepath.Join(repoRoot(t), ".github", "runner-image", "Dockerfile"))

	for _, bin := range []string{"op", "tofu", "age", "rclone", "kubectl"} {
		if !strings.Contains(dockerfile, bin) {
			t.Errorf("the runner image does not install %q, which steward shells out to", bin)
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
