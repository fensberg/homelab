package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A release of the platform moves the sites' lines in the file it is given,
// and a file already there is left exactly as it was.
func TestDeliveringThePlatformMovesTheSitesLinesAndOnlyThose(t *testing.T) {
	was := "sha256:" + strings.Repeat("a", 64)
	is := "sha256:" + strings.Repeat("b", 64)
	file := filepath.Join(t.TempDir(), "pins.json")
	body := "{\n  \"site0\": {\n    \"platform\": \"v2026.10.1@" + was + "\"\n  }\n}\n"
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"-pins", file, "-version", "v2026.10.2", "-digest", is}

	if code := deliverPlatform(args); code != 0 {
		t.Fatalf("delivering exited %d", code)
	}
	got, _ := os.ReadFile(file)
	if want := strings.Replace(body, "v2026.10.1@"+was, "v2026.10.2@"+is, 1); string(got) != want {
		t.Errorf("the file is\n%s\nand should be\n%s", got, want)
	}
	stat, _ := os.Stat(file)
	if code := deliverPlatform(args); code != 0 {
		t.Fatalf("delivering what is already there exited %d", code)
	}
	if again, _ := os.Stat(file); !again.ModTime().Equal(stat.ModTime()) {
		t.Error("a file already at the release was written again")
	}
}

// What is not a delivery is refused and writes nothing.
func TestDeliveringThePlatformRefusesWhatIsNotARelease(t *testing.T) {
	file := filepath.Join(t.TempDir(), "pins.json")
	const body = "{\n  \"site0\": {\n    \"platform\": \"not a release\"\n  }\n}\n"
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("b", 64)
	for name, c := range map[string]struct {
		args []string
		code int
	}{
		"no flag at all":                  {nil, 2},
		"no digest":                       {[]string{"-pins", file, "-version", "v2026.10.2"}, 2},
		"a flag there is not":             {[]string{"-nonsense"}, 2},
		"a file that is not there":        {[]string{"-pins", file + ".missing", "-version", "v2026.10.2", "-digest", digest}, 1},
		"a line that is no release":       {[]string{"-pins", file, "-version", "v2026.10.2", "-digest", digest}, 1},
		"a version that names no release": {[]string{"-pins", file, "-version", "latest", "-digest", digest}, 1},
	} {
		if code := deliverPlatform(c.args); code != c.code {
			t.Errorf("%s: exited %d, want %d", name, code, c.code)
		}
	}
	if got, _ := os.ReadFile(file); string(got) != body {
		t.Error("a refused delivery changed the file")
	}
}
