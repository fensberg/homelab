package repopath

import (
	"os"
	"path/filepath"
	"testing"
)

// The root is found at any depth, which is the property the eight copies it
// replaces did not have.
func TestRootIsFoundFromAnyDepth(t *testing.T) {
	root := t.TempDir()
	for _, m := range Markers {
		if err := os.WriteFile(filepath.Join(root, m), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, depth := range []string{
		root,
		filepath.Join(root, "a"),
		filepath.Join(root, "a", "b", "c", "d", "e"),
	} {
		if err := os.MkdirAll(depth, 0o700); err != nil {
			t.Fatal(err)
		}
		got, err := rootFrom(depth)
		if err != nil {
			t.Errorf("from %s: %v", depth, err)
			continue
		}
		if got != root {
			t.Errorf("from %s found %s, want %s", depth, got, root)
		}
	}
}

// Outside a checkout it says so, rather than returning some directory that
// happens to be the top of the filesystem.
func TestRootRefusesWhenThereIsNoMarker(t *testing.T) {
	if got, err := rootFrom(t.TempDir()); err == nil {
		t.Errorf("found a root at %s with no markers anywhere above it", got)
	}
}

// And against the real repository, the answer is the real repository.
func TestRootFindsThisRepository(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "management", "cluster")); err != nil {
		t.Errorf("Root() returned %s, which has no management/cluster: %v", root, err)
	}
}

// Join is Root plus a path, and lands inside the repository.
func TestJoinResolvesInsideTheRepository(t *testing.T) {
	p, err := Join("management", "cluster")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("Join(management, cluster) = %s, which does not exist: %v", p, err)
	}
}

// Both remote forms a clone can have, and the ways a remote can fail to name
// a repository. Addresses use the documented placeholders.
func TestSlugFromRemote(t *testing.T) {
	for _, tc := range []struct{ remote, want string }{
		{"https://github.com/example/estate.git\n", "example/estate"},
		{"https://github.com/example/estate", "example/estate"},
		{"git@github.com:example/estate.git", "example/estate"},
		{"ssh://git@github.com/example/estate.git", "example/estate"},
	} {
		got, err := slugFromRemote(tc.remote)
		if err != nil || got != tc.want {
			t.Errorf("slugFromRemote(%q) = %q, %v; want %q", tc.remote, got, err, tc.want)
		}
	}
	for _, bad := range []string{
		"https://gitlab.example/example/estate.git",
		"https://github.com/example",
		"https://github.com/example/estate/extra",
		"",
	} {
		if got, err := slugFromRemote(bad); err == nil {
			t.Errorf("slugFromRemote(%q) = %q with no error; it names no owner/name", bad, got)
		}
	}
}

// In Actions the environment is authoritative, whatever the remote says.
func TestSlugPrefersGitHubRepository(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "example/from-env")
	got, err := Slug()
	if err != nil || got != "example/from-env" {
		t.Errorf("Slug() = %q, %v; want the GITHUB_REPOSITORY value", got, err)
	}
}

// A directory holding only some of the markers is not the root, however close
// to the caller it is. This is the case that would otherwise shrink every guard:
// a CLAUDE.md added to a subdirectory.
func TestANestedClaudeMdDoesNotBecomeTheRoot(t *testing.T) {
	root := t.TempDir()
	for _, m := range Markers {
		if err := os.WriteFile(filepath.Join(root, m), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	nested := filepath.Join(root, "scripts", "tool")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts", "CLAUDE.md"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := rootFrom(nested)
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Errorf("a nested CLAUDE.md made %s the root instead of %s.\n\n"+
			"Every guard enumerates the repository from this answer, so each would silently "+
			"check only that subtree and go on reporting green.", got, root)
	}
}

type recorder struct{ failed bool }

func (r *recorder) Helper()      {}
func (r *recorder) Fatal(...any) { r.failed = true }

func TestRootOrFailAnswersFromInsideTheRepository(t *testing.T) {
	var r recorder
	if got := RootOrFail(&r); got == "" || r.failed {
		t.Fatalf("got %q, failed=%v", got, r.failed)
	}
}

// gitWithOrigin is a git on PATH whose origin remote is the given URL, or
// which has none.
func gitWithOrigin(t *testing.T, url string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\necho '" + url + "'\n"
	if url == "" {
		script = "#!/bin/sh\necho 'error: No such remote' >&2\nexit 2\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GITHUB_REPOSITORY", "")
}

// On a workstation there is no GITHUB_REPOSITORY, and the repository is
// whatever its origin says it is. That is what makes a fork inspect its own
// rules and not the ones of the repository it was copied from.
func TestSlugIsReadFromTheOriginRemoteOnAWorkstation(t *testing.T) {
	for _, url := range []string{"https://github.com/an-owner/a-fork.git", "git@github.com:an-owner/a-fork.git"} {
		gitWithOrigin(t, url)
		got, err := Slug()
		if err != nil || got != "an-owner/a-fork" {
			t.Errorf("origin %s read as %q, %v", url, got, err)
		}
	}
}

// With neither, there is no repository to name, and guessing one would name
// somebody else's.
func TestSlugRefusesWhenNothingSaysWhichRepositoryThisIs(t *testing.T) {
	gitWithOrigin(t, "")
	if got, err := Slug(); err == nil {
		t.Fatalf("named %q with no GITHUB_REPOSITORY and no origin", got)
	}
}

func TestSlugRefusesAnOriginThatIsNotOnGitHub(t *testing.T) {
	gitWithOrigin(t, "https://example.invalid/an-owner/a-fork.git")
	if got, err := Slug(); err == nil {
		t.Fatalf("named %q from an origin that is not on GitHub", got)
	}
}
