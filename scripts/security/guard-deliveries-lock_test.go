package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const lockSuppliers = `egress:
  - job: x.yml/y
    policy: block

tools:
  - source: bridgecrewio/checkov
    version: CHECKOV_VERSION
    pypi: checkov
  - source: hadolint/hadolint
    version: HADOLINT_VERSION
    fetch: https://github.com/hadolint/hadolint/releases/download/v{version}/hadolint-linux-x86_64
  - source: pre-commit/pre-commit
    version: PRE_COMMIT_VERSION
    pypi: pre-commit
  - source: example/undelivered
    version: UNDELIVERED_VERSION

hooks:
  - repo: https://example.com/hook
`

const lockVersions = "CHECKOV_VERSION=3.3.17\nPRE_COMMIT_VERSION=4.6.2\nHADOLINT_VERSION=2.15.1\n"

var (
	hashA = strings.Repeat("a", 64)
	hashB = strings.Repeat("b", 64)
	hashC = strings.Repeat("c", 64)
	hashD = strings.Repeat("d", 64)
)

var goodLock = `# header
# [fetch: hadolint HADOLINT_VERSION=2.15.1]
https://github.com/hadolint/hadolint/releases/download/v2.15.1/hadolint-linux-x86_64 --hash=sha256:` + hashD + `
# [end]
# [pypi: checkov CHECKOV_VERSION=3.3.17]
checkov==3.3.17 --hash=sha256:` + hashA + `
pyyaml==6.0.3 --hash=sha256:` + hashB + `
# [end]
# [pypi: pre-commit PRE_COMMIT_VERSION=4.6.2]
pre-commit==4.6.2 --hash=sha256:` + hashC + `
# [end]
`

func writeLockFixture(t *testing.T, suppliers, versions, lock string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	for p, body := range map[string]string{suppliersPath: suppliers, versionsPath: versions, lockPath: lock} {
		if body == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestALockThatMatchesItsDeclarationsPasses(t *testing.T) {
	findings, err := checkDeliveriesLock(writeLockFixture(t, lockSuppliers, lockVersions, goodLock))
	if err != nil || len(findings) != 0 {
		t.Fatalf("want a clean lock, got %v %v", findings, err)
	}
}

func TestEveryWayALockDriftsIsRefused(t *testing.T) {
	hadolintLine := "https://github.com/hadolint/hadolint/releases/download/v2.15.1/hadolint-linux-x86_64 --hash=sha256:" + hashD
	for name, tc := range map[string]struct{ suppliers, versions, lock, want string }{
		"a version bumped without ordering again": {
			lockSuppliers, strings.Replace(lockVersions, "3.3.17", "3.3.18", 1), goodLock, "the lock was made for CHECKOV_VERSION=3.3.17"},
		"a binary bumped without ordering again": {
			lockSuppliers, strings.Replace(lockVersions, "2.15.1", "2.16.0", 1), goodLock, "must be exactly one line fetching https://github.com/hadolint/hadolint/releases/download/v2.16.0/"},
		"a declared tool with no section": {
			lockSuppliers, lockVersions, strings.Replace(goodLock, "# [pypi: pre-commit PRE_COMMIT_VERSION=4.6.2]\npre-commit==4.6.2 --hash=sha256:"+hashC+"\n# [end]\n", "", 1), "pre-commit is declared as a pypi delivery and has no section"},
		"a line with no hash": {
			lockSuppliers, lockVersions, strings.Replace(goodLock, "pyyaml==6.0.3 --hash=sha256:"+hashB, "pyyaml==6.0.3", 1), "has a line with no SHA256"},
		"a hash that is not a SHA256": {
			lockSuppliers, lockVersions, strings.Replace(goodLock, hashB, "bb", 1), "has a line with no SHA256"},
		"a section missing the tool itself": {
			lockSuppliers, lockVersions, strings.Replace(goodLock, "checkov==3.3.17 --hash=sha256:"+hashA+"\n", "", 1), "does not pin checkov itself"},
		"a fetch from a different address": {
			lockSuppliers, lockVersions, strings.Replace(goodLock, hadolintLine, strings.Replace(hadolintLine, "github.com/hadolint", "example.com/hadolint", 1), 1), "must be exactly one line fetching"},
		"a fetch with a second file": {
			lockSuppliers, lockVersions, strings.Replace(goodLock, hadolintLine, hadolintLine+"\n"+hadolintLine, 1), "must be exactly one line fetching"},
		"a fetch that is not https": {
			strings.Replace(lockSuppliers, "fetch: https://", "fetch: http://", 1), lockVersions, strings.Replace(goodLock, "https://github.com/hadolint", "http://github.com/hadolint", 1), "which is not an https URL"},
		"a section for an undeclared tool": {
			lockSuppliers, lockVersions, goodLock + "# [pypi: ansible-core ANSIBLE_CORE_VERSION=2.21.3]\nansible-core==2.21.3 --hash=sha256:" + hashA + "\n# [end]\n", "the lock has a section for pypi:ansible-core, which no tools: entry declares"},
		"one name declared as two kinds": {
			lockSuppliers + "tools:\n  - source: somebody/checkov\n    version: CHECKOV_VERSION\n    fetch: https://example.com/checkov\n",
			lockVersions, goodLock, "checkov is declared as both"},
		"a section never closed": {
			lockSuppliers, lockVersions, strings.TrimSuffix(goodLock, "# [end]\n"), "never closed"},
		"no lock at all": {
			lockSuppliers, lockVersions, "", "does not exist"},
	} {
		t.Run(name, func(t *testing.T) {
			findings, err := checkDeliveriesLock(writeLockFixture(t, tc.suppliers, tc.versions, tc.lock))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(findings, "\n"), tc.want) {
				t.Fatalf("want a finding containing %q, got %v", tc.want, findings)
			}
		})
	}
}

func TestADeclarationThatCannotBeReadIsAnErrorNotAPass(t *testing.T) {
	for name, tc := range map[string]struct{ suppliers, versions string }{
		"no delivery declared":          {"tools:\n  - source: a/b\n    version: X\n", "X=1\n"},
		"a kind with no version:":       {"tools:\n  - source: a/b\n    pypi: b\n", "X=1\n"},
		"a version versions.env lacks":  {"tools:\n  - source: a/b\n    version: B_VERSION\n    pypi: b\n", "X=1\n"},
		"one entry declaring two kinds": {"tools:\n  - source: a/b\n    version: X\n    pypi: b\n    fetch: https://example.com/b\n", "X=1\n"},
		"no suppliers list at all":      {"", "X=1\n"},
		"no versions file at all":       {"tools:\n  - source: a/b\n    version: X\n    pypi: b\n", ""},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := checkDeliveriesLock(writeLockFixture(t, tc.suppliers, tc.versions, goodLock)); err == nil {
				t.Fatal("must be an error, or every delivery passes unchecked")
			}
		})
	}
}

func TestPythonNamesAreNormalisedTheWayTheLockWritesThem(t *testing.T) {
	got, err := deliveriesIn("tools:\n  - source: a/b\n    version: X\n    pypi: Ruamel.YAML\n", map[string]string{"X": "1"})
	if err != nil || len(got) != 1 || got[0].Name != "ruamel-yaml" {
		t.Fatalf("got %+v %v", got, err)
	}
}

func TestTheExplanationSaysHowToOrderAgain(t *testing.T) {
	if !strings.Contains(explainDeliveriesLock([]string{"x"}), "task order-deliveries") {
		t.Fatal("the refusal must name the command that fixes it")
	}
}

func TestParsingRefusesAMalformedLock(t *testing.T) {
	for _, body := range []string{
		"# [end]\n",
		"# [pypi: a A_VERSION=1]\n# [pypi: b B_VERSION=1]\n",
		"# [pypi: a A_VERSION=1]\na==1 --hash=sha256:x\n# [end]\n# [pypi: a A_VERSION=1]\n# [end]\n",
	} {
		if _, err := parseDeliveriesLock(body); err == nil {
			t.Errorf("want a refusal for:\n%s", body)
		}
	}
}
