package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The script that sets up every machine touching this estate, run for real.
//
// WHY. It was on tests/coverage-blocklist.yml as #292: every machine that
// reaches this estate is configured by it, and no test had ever executed a line
// of it. It has already shipped a false claim that cost an evening - it said
// registering a signing key with GitHub was optional, while the branch ruleset
// requires a VERIFIED signature, so the operator's editor pushed into a refusal
// with no readable reason.
//
// HOW, given that installing system packages is not testable. The script is
// almost entirely `if has X; then skip; else install; fi`, so with every tool
// stubbed as ALREADY PRESENT nothing installs anything and what remains is
// exactly the part that decides things: the git configuration it writes, the
// hook wiring, and the signing branch. That is the half #292 says is testable
// in isolation.
//
// covers: shell:scripts/install-dependencies.sh

// Every command the script looks for, stubbed present so no install branch is
// taken. Listed rather than discovered: a tool this forgets is one whose
// install branch runs for real, which is a slow test and a modified machine.
var setupTools = []string{
	"unzip", "go", "node", "npm", "npx", "pnpm", "corepack", "tofu", "op",
	"kubectl", "talosctl", "flux", "helm", "age", "age-keygen", "rclone",
	"task", "ansible", "ansible-playbook", "ansible-galaxy", "python3",
	"pre-commit", "checkov", "zizmor", "codespell", "hadolint", "shellcheck",
	"pipx",
}

// setupFixture is a throwaway repository the script can configure without
// touching this one.
//
// That separation is not tidiness: the script runs `git config commit.gpgsign
// true` against `$(dirname $0)/..`, so a test running it in place would rewrite
// the developer's own checkout.
type setupFixture struct {
	repo string
	env  []string
}

func newSetupFixture(t *testing.T, ghBehaviour string) *setupFixture {
	t.Helper()
	root := repoRoot(t)
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolving %s: %v", dir, err)
	}
	dir = resolved

	repo := filepath.Join(dir, "repo")
	bin := filepath.Join(dir, "bin")
	home := filepath.Join(dir, "home")
	for _, d := range []string{
		filepath.Join(repo, "scripts"),
		filepath.Join(repo, "githooks"),
		filepath.Join(repo, "management", "hypervisor"),
		bin, home, filepath.Join(home, ".ssh"),
	} {
		if mkErr := os.MkdirAll(d, 0o755); mkErr != nil {
			t.Fatalf("creating %s: %v", d, mkErr)
		}
	}

	// The script and the one file it sources, copied so `dirname $0/..` is the
	// throwaway repository rather than this one.
	for _, name := range []string{"install-dependencies.sh", "versions.env"} {
		body, readErr := os.ReadFile(filepath.Join(root, "scripts", name))
		if readErr != nil {
			t.Fatalf("reading scripts/%s: %v", name, readErr)
		}
		if wErr := os.WriteFile(filepath.Join(repo, "scripts", name), body, 0o755); wErr != nil {
			t.Fatalf("writing %s: %v", name, wErr)
		}
	}
	if wErr := os.WriteFile(filepath.Join(repo, "management", "hypervisor", "requirements.yml"),
		[]byte("collections: []\n"), 0o644); wErr != nil {
		t.Fatal(wErr)
	}

	// A stub that answers any version query and otherwise succeeds quietly.
	stub := "#!/usr/bin/env bash\ncase \"$1\" in\n  --version|-version|version) echo \"stub 0.0.0\" ;;\nesac\nexit 0\n"
	for _, tool := range setupTools {
		if wErr := os.WriteFile(filepath.Join(bin, tool), []byte(stub), 0o755); wErr != nil {
			t.Fatalf("writing the %s stub: %v", tool, wErr)
		}
	}

	// Two tools are checked by VERSION rather than by presence, so a generic
	// stub makes the script decide to replace them - and replacing needs sudo,
	// which is neither available nor wanted here.
	//
	// The versions come from the same versions.env the script sources, so this
	// cannot drift away from the pin it is impersonating. Worth noting that the
	// script's own header still claims it "checks for presence, not for a
	// specific pinned version"; that stopped being true for these two.
	pins := pinnedVersions(t, filepath.Join(root, "scripts", "versions.env"))
	for tool, format := range map[string]string{
		"rclone": "rclone v%s\n- os/version: stub\n",
		"task":   "Task version: v%s\n",
	} {
		key := strings.ToUpper(tool) + "_VERSION"
		version, ok := pins[key]
		if !ok {
			t.Fatalf("scripts/versions.env has no %s, so this fixture cannot "+
				"impersonate the pinned %s and the script would try to install one", key, tool)
		}
		body := "#!/usr/bin/env bash\nprintf '" + strings.ReplaceAll(format, "%s", version) + "'\nexit 0\n"
		if wErr := os.WriteFile(filepath.Join(bin, tool), []byte(body), 0o755); wErr != nil {
			t.Fatalf("writing the %s stub: %v", tool, wErr)
		}
	}
	if ghBehaviour != "" {
		if wErr := os.WriteFile(filepath.Join(bin, "gh"), []byte(ghBehaviour), 0o755); wErr != nil {
			t.Fatalf("writing the gh stub: %v", wErr)
		}
	}

	env := []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + home,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	}

	init := exec.Command("git", "init", "-q", repo)
	init.Env = env
	if out, initErr := init.CombinedOutput(); initErr != nil {
		t.Fatalf("git init: %v\n%s", initErr, out)
	}
	return &setupFixture{repo: repo, env: env}
}

func (f *setupFixture) run(t *testing.T) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", f.repo+"/scripts/install-dependencies.sh")
	cmd.Dir = f.repo
	cmd.Env = f.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (f *setupFixture) gitConfig(t *testing.T, key string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", f.repo, "config", "--get", key)
	cmd.Env = f.env
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// A machine with every tool present installs nothing and still configures the
// repository.
//
// The configuration is the part that matters and the part nothing checked. Hooks
// in this repository run from githooks/ rather than .git/hooks, and that is true
// only because this script sets core.hooksPath - without it the supplier guard
// is a file nobody invokes and every shim tested elsewhere is unreachable.
func TestSetupWiresTheHooksAndSigning(t *testing.T) {
	f := newSetupFixture(t, "")

	out, err := f.run(t)
	if err != nil {
		t.Fatalf("the script failed on a machine where every tool is present: %v\n%s", err, out)
	}

	if got := f.gitConfig(t, "core.hooksPath"); got != "githooks" {
		t.Errorf(`core.hooksPath is %q, not "githooks".

git then runs .git/hooks, which pre-commit owns and overwrites, so the supplier
guard in githooks/pre-commit becomes a file nobody invokes. Every shim is
unreachable and nothing says so.`, got)
	}
	if got := f.gitConfig(t, "fetch.prune"); got != "true" {
		t.Errorf("fetch.prune is %q, so remote-tracking refs for deleted branches "+
			"accumulate in every checkout forever", got)
	}
	for _, want := range []struct{ key, value string }{
		{"gpg.format", "ssh"},
		{"commit.gpgsign", "true"},
	} {
		if got := f.gitConfig(t, want.key); got != want.value {
			t.Errorf(`%s is %q, want %q.

The all-branches ruleset carries required_signatures with no bypass actors, so
an unsigned commit is refused on every branch and the refusal carries no
readable reason.`, want.key, got, want.value)
		}
	}
	if key := f.gitConfig(t, "user.signingkey"); key == "" {
		t.Error("no signing key was configured, so commits from this checkout are unsigned")
	}
}

// A machine with no GitHub user account is told so, and is not warned at.
//
// The agent has no user account by construction - there is nothing to register a
// key against, and it publishes through signedpush, which has GitHub sign on its
// behalf. A warning there would be noise on every run, and a warning that is
// always present is one nobody reads.
func TestSetupSaysNothingToRegisterWhereThereIsNoAccount(t *testing.T) {
	f := newSetupFixture(t, "#!/usr/bin/env bash\nexit 1\n")

	out, err := f.run(t)
	if err != nil {
		t.Fatalf("the script failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "nothing to register") {
		t.Errorf("the script did not say why it registered no key:\n%s", out)
	}
	if strings.Contains(out, "WILL be refused") {
		t.Errorf("the script warned about refused pushes on a machine with no user "+
			"account, which signs through signedpush rather than with a key:\n%s", out)
	}
}

// A key that cannot be registered is a loud failure naming the two commands.
//
// THE CLAIM THAT COST AN EVENING (#292). This step used to present registering
// the key as optional. It is not: the ruleset wants a VERIFIED signature, so a
// commit signed with a key GitHub does not know is refused exactly like an
// unsigned one - and the operator's editor pushed into that refusal with no
// readable reason.
func TestSetupSaysPushesWillBeRefusedWithoutARegisteredKey(t *testing.T) {
	gh := "#!/usr/bin/env bash\ncase \"$1\" in\n  api) echo someone; exit 0 ;;\n  ssh-key) [ \"$2\" = list ] && exit 0; exit 1 ;;\nesac\nexit 0\n"
	f := newSetupFixture(t, gh)

	out, err := f.run(t)
	if err != nil {
		t.Fatalf("the script failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "WILL be refused") {
		t.Errorf(`the script did not say that pushes will be refused:

%s
Registering the key is not optional. required_signatures wants a VERIFIED
signature, so a commit signed with a key GitHub does not know is refused
exactly like an unsigned one.`, out)
	}
	for _, want := range []string{"gh auth refresh", "gh ssh-key add"} {
		if !strings.Contains(out, want) {
			t.Errorf("the warning does not name %q, so it states the problem without "+
				"the remedy:\n%s", want, out)
		}
	}
}

// Re-running leaves an already-configured checkout alone.
//
// The script's own header says it is safe to re-run and that claim was untested.
// Regenerating a signing key on a second run would replace the one GitHub knows
// about, turning a working setup into refused pushes.
func TestSetupIsSafeToReRun(t *testing.T) {
	f := newSetupFixture(t, "")

	if out, err := f.run(t); err != nil {
		t.Fatalf("first run: %v\n%s", err, out)
	}
	first := f.gitConfig(t, "user.signingkey")

	out, err := f.run(t)
	if err != nil {
		t.Fatalf("second run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "commit signing already configured") {
		t.Errorf("a second run did not recognise that signing was already set up:\n%s", out)
	}
	if second := f.gitConfig(t, "user.signingkey"); second != first {
		t.Errorf(`the signing key changed on a re-run: %q -> %q.

Replacing the key GitHub knows about turns a working setup into refused pushes,
and the script says in its own header that it is safe to re-run.`, first, second)
	}
}

// pinnedVersions reads scripts/versions.env the way the script itself does.
func pinnedVersions(t *testing.T, path string) map[string]string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	pins := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok {
			pins[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	if len(pins) == 0 {
		t.Fatalf("%s declares no versions, so this fixture would impersonate nothing", path)
	}
	return pins
}
