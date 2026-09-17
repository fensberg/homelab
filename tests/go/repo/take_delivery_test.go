package repo

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The one installer the lock reaches, run for real against a lock written here.
//
// WHY. Every lane and every workstation installs its tools through
// scripts/take-delivery.sh (#416), so its one job - refuse a file whose hash
// the lock does not list - is the whole of what the lock is worth. A version of
// it that skipped the check would install everything exactly as before and
// every lane would stay green. That failure is silent by construction, which is
// why it is tested rather than read.
//
// HOW, without a network. curl and python3 are stubbed on PATH: curl serves
// files from a directory the test fills, and python3 records what pip would
// have been asked to install. Everything else - the lock parsing, the hash
// check, the archive extraction, where things land - is the shipped script.
//
// covers: shell:scripts/take-delivery.sh

type deliveryFixture struct {
	root, serve, home, ghPath, pipArgs string
	env                                []string
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func newDeliveryFixture(t *testing.T, lock string) *deliveryFixture {
	t.Helper()
	dir := t.TempDir()
	f := &deliveryFixture{
		root:    filepath.Join(dir, "repo"),
		serve:   filepath.Join(dir, "serve"),
		home:    filepath.Join(dir, "home"),
		ghPath:  filepath.Join(dir, "github-path"),
		pipArgs: filepath.Join(dir, "pip-args"),
	}
	bin := filepath.Join(dir, "bin")
	for _, d := range []string{filepath.Join(f.root, "scripts"), f.serve, f.home, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "take-delivery.sh"))
	if err != nil {
		t.Fatalf("reading scripts/take-delivery.sh: %v", err)
	}
	for name, body := range map[string]string{
		"scripts/take-delivery.sh": string(script),
		"scripts/deliveries.lock":  lock,
	} {
		if err := os.WriteFile(filepath.Join(f.root, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stubs := map[string]string{
		// Serves the file named by the URL's last element, and fails like
		// curl -f when there is none.
		"curl": "#!/usr/bin/env bash\nout=\"\"; url=\"\"\n" +
			"while [ $# -gt 0 ]; do case \"$1\" in -o) out=\"$2\"; shift 2 ;; *) url=\"$1\"; shift ;; esac; done\n" +
			"[ -f \"$SERVE/${url##*/}\" ] || exit 22\ncp \"$SERVE/${url##*/}\" \"$out\"\n",
		// Records the arguments and the requirements file pip was handed.
		"python3": "#!/usr/bin/env bash\necho \"$*\" >>\"$PIP_ARGS\"\n" +
			"while [ $# -gt 0 ]; do [ \"$1\" = -r ] && cat \"$2\" >>\"$PIP_ARGS\"; shift; done\n",
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.env = []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + f.home,
		"SERVE=" + f.serve,
		"PIP_ARGS=" + f.pipArgs,
		"GITHUB_PATH=" + f.ghPath,
	}
	return f
}

func (f *deliveryFixture) serveFile(t *testing.T, name string, body []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.serve, name), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *deliveryFixture) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{filepath.Join(f.root, "scripts", "take-delivery.sh")}, args...)...)
	cmd.Env = f.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// installed reads what take-delivery.sh put in ~/.local/bin, or nil when it
// put nothing there. Any other failure to read it is a failure of the test,
// never an answer of "not installed".
func (f *deliveryFixture) installed(t *testing.T, name string) ([]byte, os.FileInfo) {
	t.Helper()
	path := filepath.Join(f.home, ".local", "bin", name)
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		t.Fatalf("checking whether %s was installed: %v", name, err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the installed %s: %v", name, err)
	}
	return body, info
}

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf strings.Builder
	gz := gzip.NewWriter(&stringWriter{&buf})
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return []byte(buf.String())
}

type stringWriter struct{ b *strings.Builder }

func (w *stringWriter) Write(p []byte) (int, error) { return w.b.Write(p) }

func TestTakeDeliveryInstallsOnlyTheFileTheLockWasOrderedWith(t *testing.T) {
	binary := []byte("#!/bin/sh\necho the real tool\n")
	lock := "# [fetch: tool TOOL_VERSION=1.0]\n" +
		"https://example.com/v1.0/tool-linux-x86_64 --hash=sha256:" + sha256Hex(binary) + "\n# [end]\n"

	t.Run("the ordered file is installed", func(t *testing.T) {
		f := newDeliveryFixture(t, lock)
		f.serveFile(t, "tool-linux-x86_64", binary)
		if out, err := f.run(t, "tool"); err != nil {
			t.Fatalf("take-delivery.sh refused the file it was ordered with: %v\n%s", err, out)
		}
		body, info := f.installed(t, "tool")
		if string(body) != string(binary) {
			t.Fatalf("~/.local/bin/tool is %q, not the file that was served", body)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Errorf("the tool was installed without the execute bit (%v)", info.Mode())
		}
		ghPath, _ := os.ReadFile(f.ghPath)
		if !strings.Contains(string(ghPath), filepath.Join(f.home, ".local", "bin")) {
			t.Error("~/.local/bin was not added to GITHUB_PATH, so a later step cannot find the tool by name")
		}
	})

	t.Run("a different file is refused and nothing is installed", func(t *testing.T) {
		f := newDeliveryFixture(t, lock)
		f.serveFile(t, "tool-linux-x86_64", []byte("#!/bin/sh\necho something else\n"))
		out, err := f.run(t, "tool")
		if err == nil {
			t.Fatalf("take-delivery.sh installed a file whose hash the lock does not list:\n%s", out)
		}
		if !strings.Contains(out, "is not the file scripts/deliveries.lock was ordered with") {
			t.Errorf("the refusal does not say why:\n%s", out)
		}
		if body, _ := f.installed(t, "tool"); body != nil {
			t.Errorf("a refused file was installed anyway: %q", body)
		}
	})
}

func TestTakeDeliveryTakesTheNamedFileOutOfAnArchive(t *testing.T) {
	archive := tarGz(t, map[string]string{"LICENSE": "text", "tool": "#!/bin/sh\necho from the archive\n"})
	lock := "# [fetch: tool TOOL_VERSION=2.0]\n" +
		"https://example.com/v2.0/tool_2.0_linux_amd64.tar.gz --hash=sha256:" + sha256Hex(archive) + "\n# [end]\n"
	f := newDeliveryFixture(t, lock)
	f.serveFile(t, "tool_2.0_linux_amd64.tar.gz", archive)
	if out, err := f.run(t, "tool"); err != nil {
		t.Fatalf("take-delivery.sh could not take the tool out of its archive: %v\n%s", err, out)
	}
	if body, _ := f.installed(t, "tool"); !strings.Contains(string(body), "from the archive") {
		t.Fatalf("~/.local/bin/tool is %q, not the archive's tool", body)
	}
	if body, _ := f.installed(t, "LICENSE"); body != nil {
		t.Error("the archive's other files were installed as tools")
	}
}

func TestTakeDeliveryHandsPipEveryHashAndEachSharedLineOnce(t *testing.T) {
	lock := "# [pypi: alpha ALPHA_VERSION=1]\n" +
		"alpha==1 --hash=sha256:" + strings.Repeat("a", 64) + "\n" +
		"shared==3 --hash=sha256:" + strings.Repeat("c", 64) + "\n# [end]\n" +
		"# [pypi: beta BETA_VERSION=2]\n" +
		"beta==2 --hash=sha256:" + strings.Repeat("b", 64) + "\n" +
		"shared==3 --hash=sha256:" + strings.Repeat("c", 64) + "\n# [end]\n"
	f := newDeliveryFixture(t, lock)
	if out, err := f.run(t, "alpha", "beta"); err != nil {
		t.Fatalf("take-delivery.sh failed: %v\n%s", err, out)
	}
	asked, err := os.ReadFile(f.pipArgs)
	if err != nil {
		t.Fatal("pip was never run")
	}
	got := string(asked)
	if !strings.Contains(got, "--require-hashes") {
		t.Errorf("pip was not told to require hashes, so it installs whatever it resolves:\n%s", got)
	}
	for _, want := range []string{"alpha==1", "beta==2"} {
		if !strings.Contains(got, want) {
			t.Errorf("pip was not asked for %s:\n%s", want, got)
		}
	}
	if strings.Count(got, "shared==3") != 1 {
		t.Errorf("a line shared by two sections reached pip %d times, and pip refuses a duplicate requirement:\n%s",
			strings.Count(got, "shared==3"), got)
	}
}

func TestTakeDeliveryRefusesWhatTheLockDoesNotDecide(t *testing.T) {
	script := []byte("#!/bin/sh\ntouch \"$HOME/ran\"\n")
	lock := "# [fetch: stranger STRANGER_VERSION=1]\n" +
		"https://example.com/install.sh --hash=sha256:" + sha256Hex(script) + "\n# [end]\n" +
		"# [pypi: alpha ALPHA_VERSION=1]\nalpha==1 --hash=sha256:" + strings.Repeat("a", 64) + "\n# [end]\n"

	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"a name the lock does not have":        {[]string{"missing"}, "is not in scripts/deliveries.lock"},
		"a name that is not a name":            {[]string{"Bad;name"}, "is not a delivery name"},
		"no name at all":                       {nil, "usage:"},
		"an installer nothing says how to run": {[]string{"stranger"}, "nothing here says how to run it"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newDeliveryFixture(t, lock)
			f.serveFile(t, "install.sh", script)
			out, err := f.run(t, tc.args...)
			if err == nil {
				t.Fatalf("take-delivery.sh %v succeeded:\n%s", tc.args, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("want output containing %q, got:\n%s", tc.want, out)
			}
			if _, statErr := os.Stat(filepath.Join(f.home, "ran")); statErr == nil {
				t.Error("an installer script ran with flags nobody wrote down")
			}
		})
	}

	t.Run("--print installs nothing", func(t *testing.T) {
		f := newDeliveryFixture(t, lock)
		out, err := f.run(t, "--print", "alpha")
		if err != nil || !strings.Contains(out, "alpha==1 --hash=sha256:") {
			t.Fatalf("--print did not print the section: %v\n%s", err, out)
		}
		if _, statErr := os.Stat(f.pipArgs); statErr == nil {
			t.Error("--print ran pip")
		}
	})
}
