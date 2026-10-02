package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/details/applications"
)

func declareApp(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(applications.Dir), name, applications.Declaration)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Every application that declares an upstream is listed with where to ask
// and the line to move, in its own pins file; one that declares none is not.
func TestUpstreamsListsWhatEachApplicationDeclares(t *testing.T) {
	root := t.TempDir()
	if got, err := upstreams(root); err != nil || len(got) != 0 {
		t.Fatalf("a repository with no applications: %v, %v", got, err)
	}
	declareApp(t, root, "zeta", `{"upstream": {"kind": "steam", "app": "10", "news": "20", "pin": "ZETA_BUILD_VERSION"}}`)
	declareApp(t, root, "quiet", `{}`)
	declareApp(t, root, "alpha", `{"upstream": {"kind": "steam", "app": "30", "news": "40", "pin": "ALPHA_BUILD_VERSION"}}`)
	got, err := upstreams(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []upstream{
		{Name: "alpha", App: "30", News: "40", Pin: "ALPHA_BUILD_VERSION", Pins: applications.Dir + "/alpha/" + applications.Pins},
		{Name: "zeta", App: "10", News: "20", Pin: "ZETA_BUILD_VERSION", Pins: applications.Dir + "/zeta/" + applications.Pins},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("upstream %d is %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A declaration that does not read fails the listing, so an application is
// never dropped from expediting in silence.
func TestUpstreamsRefusesADeclarationItCannotRead(t *testing.T) {
	root := t.TempDir()
	declareApp(t, root, "thing", `{"upstream": {"kind": "apt", "app": "3", "news": "4", "pin": "A_VERSION"}}`)
	if _, err := upstreams(root); err == nil || !strings.Contains(err.Error(), "only kind") {
		t.Errorf("an upstream of a kind nothing can ask was listed: %v", err)
	}
	if code := expediteUpstreams([]string{"-root", root}); code != 1 {
		t.Errorf("the verb exited %d on it, want 1", code)
	}
}

// The verb prints the list as one line of JSON, an empty list included: the
// workflow reads it with jq, and "no application has an upstream" has to
// read as nothing to do rather than as nothing printed.
func TestTheUpstreamsVerbPrintsJSON(t *testing.T) {
	root := t.TempDir()
	declareApp(t, root, "thing", `{"upstream": {"kind": "steam", "app": "11", "news": "21", "pin": "THING_STEAM_VERSION"}}`)
	for wantOut, dir := range map[string]string{
		`[{"name":"thing","app":"11","news":"21","pin":"THING_STEAM_VERSION","pins":"` + applications.Dir + `/thing/` + applications.Pins + `"}]`: root,
		`[]`: t.TempDir(),
	} {
		out := captureStdout(t, func() {
			if code := expediteUpstreams([]string{"-root", dir}); code != 0 {
				t.Errorf("exited %d", code)
			}
		})
		if strings.TrimSpace(out) != wantOut {
			t.Errorf("printed %q, want %q", out, wantOut)
		}
	}
	if code := expediteUpstreams([]string{"-no-such-flag"}); code != 2 {
		t.Errorf("a flag it does not have exited %d, want 2", code)
	}
}

func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	f()
	w.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return b.String()
}
