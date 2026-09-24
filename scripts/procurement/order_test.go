package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const suppliersFixture = `egress:
  - job: x.yml/y
    policy: block

tools:
  - source: bridgecrewio/checkov
    version: CHECKOV_VERSION
    pypi: checkov
    reason: >-
      Policy scanning.
  - source: hadolint/hadolint
    version: HADOLINT_VERSION
    fetch: https://github.com/hadolint/hadolint/releases/download/v{version}/hadolint-linux-x86_64
    reason: A binary, not a Python package.
  - source: example/undelivered
    version: UNDELIVERED_VERSION
    reason: Declares no kind, so it is not ordered.
  - source: pre-commit/pre-commit
    version: PRE_COMMIT_VERSION
    pypi: pre-commit

hooks:
  - repo: https://example.com/hook
`

const fixtureVersions = "CHECKOV_VERSION=3.3.17\nPRE_COMMIT_VERSION=4.6.2\nHADOLINT_VERSION=2.15.1\n"

func TestDeliveriesAreReadFromTheToolsSectionOnly(t *testing.T) {
	got, err := deliveriesIn(suppliersFixture, parseVersions(fixtureVersions))
	if err != nil {
		t.Fatal(err)
	}
	want := []declaredDelivery{
		{"fetch", "hadolint", "hadolint/hadolint", "https://github.com/hadolint/hadolint/releases/download/v{version}/hadolint-linux-x86_64", "", "HADOLINT_VERSION", "2.15.1"},
		{"pypi", "checkov", "bridgecrewio/checkov", "checkov", "", "CHECKOV_VERSION", "3.3.17"},
		{"pypi", "pre-commit", "pre-commit/pre-commit", "pre-commit", "", "PRE_COMMIT_VERSION", "4.6.2"},
	}
	if len(got) != len(want) {
		t.Fatalf("want the three entries declaring a kind, got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestADeliveryThatCannotBeOrderedIsRefused(t *testing.T) {
	_, err := deliveriesIn(suppliersFixture, map[string]string{"CHECKOV_VERSION": "3.3.17", "HADOLINT_VERSION": "1"})
	if err == nil || !strings.Contains(err.Error(), "PRE_COMMIT_VERSION") {
		t.Fatalf("want a refusal naming the missing key, got %v", err)
	}
	_, err = deliveriesIn("tools:\n  - source: a/b\n    pypi: b\n", nil)
	if err == nil || !strings.Contains(err.Error(), "no version:") {
		t.Fatalf("want a refusal for an entry with no version:, got %v", err)
	}
	_, err = deliveriesIn("tools:\n  - source: a/b\n    version: B\n    pypi: b\n    fetch: https://x/b\n", map[string]string{"B": "1"})
	if err == nil || !strings.Contains(err.Error(), "arrives one way") {
		t.Fatalf("want a refusal for an entry declaring two kinds, got %v", err)
	}
}

func TestAFetchURLIsHTTPSOnceTheVersionIsIn(t *testing.T) {
	d := declaredDelivery{Kind: "fetch", Source: "a/b", Version: "1.2"}
	for spec, want := range map[string]string{
		"https://example.com/v{version}/b-{version}.tar.gz": "https://example.com/v1.2/b-1.2.tar.gz",
		"https://example.com/install.sh":                    "https://example.com/install.sh",
	} {
		d.Spec = spec
		if got, err := fetchURL(d); err != nil || got != want {
			t.Errorf("fetchURL(%q) = %q, %v; want %q", spec, got, err, want)
		}
	}
	for _, spec := range []string{"http://example.com/b", "file:///etc/passwd", "https://example.com/{VERSION}", "b-{version}", "https:///nohost"} {
		d.Spec = spec
		if _, err := fetchURL(d); err == nil {
			t.Errorf("fetchURL(%q) must be refused", spec)
		}
	}
}

func TestFetchHashesExactlyWhatWasServed(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte("hello"))
		case "/empty":
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	saved := http.DefaultTransport
	http.DefaultTransport = srv.Client().Transport
	defer func() { http.DefaultTransport = saved }()

	got, err := fetchHash(srv.URL + "/ok")
	if err != nil || got != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Fatalf("fetchHash = %q, %v; want the SHA256 of \"hello\"", got, err)
	}
	if _, err := fetchHash(srv.URL + "/missing"); err == nil {
		t.Error("a 404 must be refused, not hashed")
	}
	if _, err := fetchHash(srv.URL + "/empty"); err == nil {
		t.Error("an empty file must be refused: its hash locks nothing")
	}
}

func TestVersionsAreReadAsKeyValuePairs(t *testing.T) {
	got := parseVersions("# comment\nA_VERSION=1.2\n\nB_SHA256 = abc\nnot a pair\n")
	if got["A_VERSION"] != "1.2" || got["B_SHA256"] != "abc" || len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}

func TestNamesAreNormalisedPerPEP503(t *testing.T) {
	for in, want := range map[string]string{"PyYAML": "pyyaml", "ruamel.yaml": "ruamel-yaml", "a__b--c": "a-b-c"} {
		if got := normalizeName(in); got != want {
			t.Errorf("normalizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func set(pkgs ...resolvedPackage) map[string]resolvedPackage {
	out := map[string]resolvedPackage{}
	for _, p := range pkgs {
		out[normalizeName(p.Name)] = p
	}
	return out
}

func TestAClosureFollowsRequirementsAndOnlyRequestedExtras(t *testing.T) {
	s := set(
		resolvedPackage{"tool", "1", []string{"Lib[fast] >=1", "unused-extra; extra == 'docs'", "absent ; sys_platform == 'win32'"}},
		resolvedPackage{"lib", "2", []string{"speedup; extra == \"fast\"", "base"}},
		resolvedPackage{"speedup", "3", nil},
		resolvedPackage{"base", "4", nil},
		resolvedPackage{"unused-extra", "5", nil},
		resolvedPackage{"other-tool", "6", nil},
	)
	got, err := closureOf("tool", s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "base,lib,speedup,tool" {
		t.Fatalf("closure = %v", got)
	}
	if _, err := closureOf("missing", s); err == nil {
		t.Fatal("a tool pip did not resolve must be refused, not given an empty closure")
	}
}

func TestResolutionsThatDisagreeAreListed(t *testing.T) {
	a := set(resolvedPackage{"x", "1", nil}, resolvedPackage{"y", "1", nil})
	b := set(resolvedPackage{"x", "2", nil}, resolvedPackage{"z", "1", nil})
	got := strings.Join(differences(a, b), "|")
	for _, want := range []string{"x 1 against 2", "y 1 only on the first", "z 1 only on the second"} {
		if !strings.Contains(got, want) {
			t.Errorf("differences missing %q: %s", want, got)
		}
	}
	if len(differences(a, a)) != 0 {
		t.Error("identical resolutions must not differ")
	}
}

func TestPipsReportIsReadIntoNormalisedPackages(t *testing.T) {
	got, err := parsePipReport([]byte(`{"install":[{"metadata":{"name":"PyYAML","version":"6.0.3","requires_dist":["a"]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if p := got["pyyaml"]; p.Version != "6.0.3" || len(p.Requires) != 1 {
		t.Fatalf("got %+v", got)
	}
	if _, err := parsePipReport([]byte(`{"install":[]}`)); err == nil {
		t.Fatal("a report that installs nothing must be refused, or the lock is empty and reads as locked")
	}
	if _, err := parsePipReport([]byte(`not json`)); err == nil {
		t.Fatal("an unreadable report must be refused")
	}
}

func TestEveryFileDigestPyPIServesIsKept(t *testing.T) {
	got, err := parsePypiRelease([]byte(`{"urls":[{"digests":{"sha256":"bb"}},{"digests":{"sha256":"aa"}},{"digests":{"sha256":"aa"}},{"digests":{}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "aa,bb" {
		t.Fatalf("got %v", got)
	}
	if _, err := parsePypiRelease([]byte(`{`)); err == nil {
		t.Fatal("an unreadable release must be refused")
	}
}

// The whole path, with pip and PyPI replaced.
func TestTheLockIsRenderedFromTheDeclarations(t *testing.T) {
	root := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "scripts"), 0o755))
	must(os.WriteFile(filepath.Join(root, suppliersPath), []byte(suppliersFixture), 0o644))
	must(os.WriteFile(filepath.Join(root, versionsPath), []byte(fixtureVersions), 0o644))

	resolved := set(
		resolvedPackage{"checkov", "3.3.17", []string{"PyYAML>=6"}},
		resolvedPackage{"pre-commit", "4.6.2", []string{"pyyaml", "cfgv"}},
		resolvedPackage{"PyYAML", "6.0.3", nil},
		resolvedPackage{"cfgv", "3.4.0", nil},
	)
	var asked []string
	resolve := func(py string, reqs []string) (map[string]resolvedPackage, error) {
		asked = append(asked, py+":"+strings.Join(reqs, " "))
		return resolved, nil
	}
	hashes := func(name, version string) ([]string, error) { return []string{"h-" + normalizeName(name)}, nil }
	var fetched []string
	fetch := func(u string) (string, error) { fetched = append(fetched, u); return "f-hash", nil }

	got, err := renderDeliveriesLock(root, resolve, hashes, fetch)
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != len(lockedPythons) || !strings.Contains(asked[0], "checkov==3.3.17 pre-commit==4.6.2") {
		t.Fatalf("resolution was asked %v", asked)
	}
	for _, want := range []string{
		"# [pypi: checkov CHECKOV_VERSION=3.3.17]\ncheckov==3.3.17 --hash=sha256:h-checkov\npyyaml==6.0.3 --hash=sha256:h-pyyaml\n# [end]\n",
		"# [pypi: pre-commit PRE_COMMIT_VERSION=4.6.2]\ncfgv==3.4.0 --hash=sha256:h-cfgv\npre-commit==4.6.2 --hash=sha256:h-pre-commit\npyyaml==6.0.3 --hash=sha256:h-pyyaml\n# [end]\n",
		"# [fetch: hadolint HADOLINT_VERSION=2.15.1]\nhttps://github.com/hadolint/hadolint/releases/download/v2.15.1/hadolint-linux-x86_64 --hash=sha256:f-hash\n# [end]\n",
		"GENERATED - do not edit",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lock is missing:\n%s\n\ngot:\n%s", want, got)
		}
	}

	if len(fetched) != 1 {
		t.Fatalf("want one fetch, got %v", fetched)
	}

	// Every way the inputs can make a lock untrustworthy is a refusal.
	calls := 0
	disagree := func(py string, reqs []string) (map[string]resolvedPackage, error) {
		calls++
		if calls == 2 {
			return set(resolvedPackage{"checkov", "3.3.17", nil}), nil
		}
		return resolved, nil
	}
	if _, err := renderDeliveriesLock(root, disagree, hashes, fetch); err == nil || !strings.Contains(err.Error(), "resolve differently") {
		t.Errorf("interpreters that disagree must be refused, got %v", err)
	}
	stray := func(string, []string) (map[string]resolvedPackage, error) {
		s := set(resolvedPackage{"orphan", "1", nil})
		for k, v := range resolved {
			s[k] = v
		}
		return s, nil
	}
	if _, err := renderDeliveriesLock(root, stray, hashes, fetch); err == nil || !strings.Contains(err.Error(), "no tool's dependency closure reaches") {
		t.Errorf("a resolved package no closure reaches must be refused, got %v", err)
	}
	if _, err := renderDeliveriesLock(root, func(string, []string) (map[string]resolvedPackage, error) {
		return nil, errors.New("pip broke")
	}, hashes, fetch); err == nil {
		t.Error("a failed resolution must be refused")
	}
	if _, err := renderDeliveriesLock(root, resolve, func(string, string) ([]string, error) { return nil, nil }, fetch); err == nil {
		t.Error("a release with no files must be refused, or its line has no hash and pip refuses it at install time")
	}
	if _, err := renderDeliveriesLock(root, resolve, func(string, string) ([]string, error) { return nil, errors.New("offline") }, fetch); err == nil {
		t.Error("a failed hash lookup must be refused")
	}
	if _, err := renderDeliveriesLock(root, resolve, hashes, func(string) (string, error) { return "", errors.New("gone") }); err == nil {
		t.Error("a failed fetch must be refused")
	}
	must(os.WriteFile(filepath.Join(root, suppliersPath), []byte("tools:\n  - source: a/b\n    version: X\n"), 0o644))
	if _, err := renderDeliveriesLock(root, resolve, hashes, fetch); err == nil || !strings.Contains(err.Error(), "nothing to order") {
		t.Errorf("no declared delivery must be refused, got %v", err)
	}
}

// A tool whose binary is not named for its repository says so, and the lock
// section - which is what take-delivery.sh is asked for - carries that name.
// fluxcd/flux2 ships a binary called flux; named flux2, its archive holds no
// such file and every install is refused.
func TestABinaryNamedUnlikeItsRepositoryIsDeliveredByThatName(t *testing.T) {
	suppliers := "tools:\n  - source: fluxcd/flux2\n    version: FLUX_VERSION\n    binary: flux\n" +
		"    fetch: https://github.com/fluxcd/flux2/releases/download/v{version}/flux_{version}_linux_amd64.tar.gz\n"
	got, err := deliveriesIn(suppliers, map[string]string{"FLUX_VERSION": "2.7.1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "flux" {
		t.Fatalf("want one delivery named flux, got %+v", got)
	}
}
