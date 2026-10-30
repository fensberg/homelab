package repo

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"homelab/tests/parsed"
	"io/fs"
	"os"
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
	root                         *parsed.Tree
	serve, home, ghPath, pipArgs string
	env                          []string
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func newDeliveryFixture(t *testing.T, lock string) *deliveryFixture {
	t.Helper()
	dir := t.TempDir()
	f := &deliveryFixture{
		root:    repository(t).CopyOf(t, "scripts/take-delivery.sh"),
		serve:   filepath.Join(dir, "serve"),
		home:    filepath.Join(dir, "home"),
		ghPath:  filepath.Join(dir, "github-path"),
		pipArgs: filepath.Join(dir, "pip-args"),
	}
	bin := filepath.Join(dir, "bin")
	for _, d := range []string{f.serve, f.home, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.root.Write(t, "scripts/deliveries.lock", lock)
	stubs := map[string]string{
		// Serves the file named by the URL's last element, and fails like
		// curl -f when there is none.
		"curl": "#!/usr/bin/env bash\nout=\"\"; url=\"\"\n" +
			"while [ $# -gt 0 ]; do case \"$1\" in -o) out=\"$2\"; shift 2 ;; *) url=\"$1\"; shift ;; esac; done\n" +
			"[ -f \"$SERVE/${url##*/}\" ] || exit 22\ncp \"$SERVE/${url##*/}\" \"$out\"\n",
		// Records the arguments and the requirements file pip was handed.
		// The system python3 only creates the environment; the environment's
		// python - a copy of this stub - records what pip was asked for, and
		// reports one command per distribution, which it also creates.
		"python3": "#!/usr/bin/env bash\n" +
			"if [ \"$1\" = -m ] && [ \"$2\" = venv ]; then mkdir -p \"$3/bin\"; cp \"$0\" \"$3/bin/python\"; exit 0; fi\n" +
			"if [ \"$1\" = -c ] && [ -z \"$2\" ]; then exit 0; fi\n" +
			"if [ \"$1\" = -m ] && [ \"$2\" = pip ]; then echo \"$0 $*\" >>\"$PIP_ARGS\"\n" +
			"  while [ $# -gt 0 ]; do [ \"$1\" = -r ] && cat \"$2\" >>\"$PIP_ARGS\"; shift; done; exit 0; fi\n" +
			"if [ \"$1\" = -c ]; then d=\"$(dirname \"$0\")\"; printf '#!/bin/sh\\necho %s\\n' \"$3\" >\"$d/$3-cmd\"; chmod +x \"$d/$3-cmd\"; echo \"$3-cmd\"; exit 0; fi\n" +
			"exit 1\n",
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
	stdout, stderr, err := f.root.Invoke(parsed.Run{Env: f.env, Bare: true, Outside: true}, "bash", append([]string{parsed.Here("scripts/take-delivery.sh")}, args...)...)
	return string(stdout) + string(stderr), err
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
	light(t)
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

// A delivery that is already here, from the lock lines it would be taken from
// now, is not fetched again: that is what lets a machine be built with its
// tools in it and still run the step that installs them. A lock that has
// moved since is a different delivery and is fetched; and a run that was
// refused records nothing, so what it did not install is not taken as here.
func TestTakeDeliveryDoesNotFetchWhatIsAlreadyHereAsTheLockOrdersIt(t *testing.T) {
	light(t)
	binary := []byte("#!/bin/sh\necho the first tool\n")
	lockFor := func(version string, body []byte) string {
		return "# [fetch: tool TOOL_VERSION=" + version + "]\n" +
			"https://example.com/v" + version + "/tool-linux-x86_64 --hash=sha256:" + sha256Hex(body) + "\n# [end]\n"
	}
	f := newDeliveryFixture(t, lockFor("1.0", binary))
	f.serveFile(t, "tool-linux-x86_64", binary)
	if out, err := f.run(t, "tool"); err != nil {
		t.Fatalf("the first delivery failed: %v\n%s", err, out)
	}

	// Nothing is served any more, so a second fetch could only fail.
	if err := os.Remove(filepath.Join(f.serve, "tool-linux-x86_64")); err != nil {
		t.Fatal(err)
	}
	out, err := f.run(t, "tool")
	if err != nil {
		t.Fatalf("a delivery that is already here was fetched again: %v\n%s", err, out)
	}
	if body, _ := f.installed(t, "tool"); string(body) != string(binary) {
		t.Errorf("the tool that was here is now %q", body)
	}
	ghPath, _ := os.ReadFile(f.ghPath)
	if strings.Count(string(ghPath), filepath.Join(f.home, ".local", "bin")) != 2 {
		t.Errorf("a run that fetched nothing did not put ~/.local/bin on GITHUB_PATH, so a later step cannot find what is here:\n%s", ghPath)
	}

	moved := []byte("#!/bin/sh\necho the second tool\n")
	f.root.Write(t, "scripts/deliveries.lock", lockFor("2.0", moved))
	if out, err := f.run(t, "tool"); err == nil {
		t.Fatalf("the lock moved and the old tool was kept as the new one:\n%s", out)
	}
	f.serveFile(t, "tool-linux-x86_64", moved)
	if out, err := f.run(t, "tool"); err != nil {
		t.Fatalf("the moved delivery failed: %v\n%s", err, out)
	}
	if body, _ := f.installed(t, "tool"); string(body) != string(moved) {
		t.Errorf("the lock moved and ~/.local/bin/tool is still %q", body)
	}

	// A refused delivery is not on record: asked for again, it is fetched
	// again, and refused again.
	refused := newDeliveryFixture(t, lockFor("1.0", binary))
	refused.serveFile(t, "tool-linux-x86_64", []byte("#!/bin/sh\necho something else\n"))
	if out, err := refused.run(t, "tool"); err == nil {
		t.Fatalf("a file the lock does not list was accepted:\n%s", out)
	}
	if out, err := refused.run(t, "tool"); err == nil {
		t.Fatalf("a file the lock does not list was refused, then taken as already here:\n%s", out)
	}
}

func TestTakeDeliveryTakesTheNamedFileOutOfAnArchive(t *testing.T) {
	light(t)
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
	light(t)
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
	// Into the isolated environment, never the user site (#423): pip accepts a
	// package the OS already has without comparing its hash, and the user site
	// replaces the OS's own packages for every Python program the account runs.
	venvPython := filepath.Join(f.home, ".local", "share", "deliveries", "pypi", "bin", "python")
	if !strings.HasPrefix(got, venvPython+" ") {
		t.Errorf("pip did not run from the isolated environment %s:\n%s", venvPython, got)
	}
	if strings.Contains(got, "--user") || strings.Contains(got, "--break-system-packages") {
		t.Errorf("pip installed into the user site, where the OS's packages satisfy the lock unhashed:\n%s", got)
	}
	wrapper, err := os.ReadFile(filepath.Join(f.home, ".local", "bin", "delivered-python"))
	if err != nil || !strings.Contains(string(wrapper), "exec \""+venvPython+"\"") {
		t.Errorf("~/.local/bin/delivered-python does not run the environment's python: %v\n%s", err, wrapper)
	}
	for _, tool := range []string{"alpha", "beta"} {
		link, err := os.Readlink(filepath.Join(f.home, ".local", "bin", tool+"-cmd"))
		if err != nil || !strings.HasPrefix(link, filepath.Dir(venvPython)) {
			t.Errorf("%s's command was not linked from the environment into ~/.local/bin: %q %v", tool, link, err)
		}
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
	light(t)
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

// A library goes into the same environment as the tools that import it, by
// hash like them, and puts nothing on PATH: it has no command, and the
// refusal of a tool that installed none must not be what it meets.
func TestTakeDeliveryPutsALibraryBesideTheToolsThatImportIt(t *testing.T) {
	lock := "# [pypi: alpha ALPHA_VERSION=1]\n" +
		"alpha==1 --hash=sha256:" + strings.Repeat("a", 64) + "\n# [end]\n" +
		"# [library: lib LIB_VERSION=4]\n" +
		"lib==4 --hash=sha256:" + strings.Repeat("d", 64) + "\n# [end]\n"
	f := newDeliveryFixture(t, lock)
	if out, err := f.run(t, "alpha", "lib"); err != nil {
		t.Fatalf("take-delivery.sh failed: %v\n%s", err, out)
	}
	asked, err := os.ReadFile(f.pipArgs)
	if err != nil {
		t.Fatal("pip was never run")
	}
	if strings.Count(string(asked), "--require-hashes") != 1 || !strings.Contains(string(asked), "lib==4 --hash=sha256:") {
		t.Errorf("the library was not installed by hash in the one pip run with its tool:\n%s", asked)
	}
	if body, _ := f.installed(t, "lib-cmd"); body != nil {
		t.Error("a library was given a command on PATH")
	}
	if body, _ := f.installed(t, "alpha-cmd"); body == nil {
		t.Error("the tool beside the library lost its command")
	}
}
