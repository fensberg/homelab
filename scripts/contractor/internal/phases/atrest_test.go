package phases

import (
	"errors"
	"fmt"
	"homelab/details/capacity"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func cluster(ready int, phase string) string {
	return `{"items":[{"spec":{"instances":3},"status":{"readyInstances":` + string(rune('0'+ready)) + `,"phase":"` + phase + `"}}]}`
}

// A database is at rest when its operator is doing nothing to it and every
// instance is ready. A port that answers says neither.
func TestADatabaseBeingRestartedIsNotAtRest(t *testing.T) {
	if err := databaseMoving([]byte(cluster(3, atRestHealthy))); err != nil {
		t.Errorf("a database with every instance ready and nothing being done to it is said to be moving: %v", err)
	}
	for _, moving := range []string{
		cluster(3, "Upgrading cluster"),
		cluster(3, "Switchover in progress"),
		cluster(2, atRestHealthy),
		cluster(3, ""),
	} {
		if err := databaseMoving([]byte(moving)); err == nil || !strings.Contains(err.Error(), "not at rest") {
			t.Errorf("%s was read as a database at rest: %v", moving, err)
		}
	}
	if err := databaseMoving([]byte(capacity.Nothing)); err == nil {
		t.Error("a cluster with no state database was read as one at rest")
	}
	if err := databaseMoving([]byte(`no`)); err == nil {
		t.Error("an answer that is not a list was read as a database at rest")
	}
}

func kustomizations(revisions ...string) string {
	var items []string
	for i, r := range revisions {
		items = append(items, `{"kind":"Kustomization","metadata":{"name":"k`+string(rune('0'+i))+`"},"status":{"lastAppliedRevision":"`+r+`"}}`)
	}
	return `{"items":[` + strings.Join(items, ",") + `]}`
}

// Flux has applied a commit when every Kustomization that follows the
// repository says it has. One that follows something else is not asked, and
// none following the repository is not the same as all of them being there.
func TestFluxHasAppliedACommitWhenEveryFollowerOfTheRepositorySaysSo(t *testing.T) {
	const commit = "c4e28c649222bb817318477ba8422f2936a6fb02"
	if err := fluxBehind([]byte(kustomizations("main@sha1:"+commit, "main@sha1:"+commit, "sha256:2b5f20aa")), commit); err != nil {
		t.Errorf("every follower of the repository is at the commit, and Flux is said to be behind: %v", err)
	}
	err := fluxBehind([]byte(kustomizations("main@sha1:"+commit, "main@sha1:6237ef6000000000000000000000000000000000")), commit)
	if err == nil || !strings.Contains(err.Error(), "k1 is at 6237ef6") || strings.Contains(err.Error(), "k0") {
		t.Errorf("the one Kustomization still at the commit before was not named, or the other was: %v", err)
	}
	if err := fluxBehind([]byte(kustomizations("sha256:2b5f20aa")), commit); err == nil || !strings.Contains(err.Error(), "none can be shown") {
		t.Errorf("no Kustomization following the repository was read as Flux having applied the commit: %v", err)
	}
	if err := fluxBehind([]byte(`no`), commit); err == nil {
		t.Error("an answer that is not a list was read as Flux having applied the commit")
	}
}

// A converge asks the cluster before it applies anything, and a plan or a
// record does not: they only read the state, and wait for nothing.
func TestOnlyAConvergeWaitsForItsStateDatabaseToBeAtRest(t *testing.T) {
	f := newVerbFixture(t)
	asked := filepath.Join(f.dir, "asked-of-the-cluster")
	head, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Skipf("these tests are not being run in a checkout, so there is no commit to ask Flux about: %v", err)
	}
	// A cluster that has applied this commit and whose database is at rest.
	kubectl := "#!/bin/sh\necho \"$*\" >> " + asked + "\ncase \"$*\" in\n" +
		"  *kustomizations*) echo '" + kustomizations("main@sha1:"+strings.TrimSpace(string(head))) + "' ;;\n" +
		"  *clusters*) echo '" + cluster(3, atRestHealthy) + "' ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(f.dir, "kubectl"), []byte(kubectl), 0o755); err != nil {
		t.Fatal(err)
	}
	questions := func() string {
		raw, _ := os.ReadFile(asked)
		_ = os.Remove(asked)
		return string(raw)
	}

	for _, root := range f.ctx.Roots() {
		_ = os.Remove(root.LocalState)
	}
	if err := Run(f.ctx, "take-over"); err != nil {
		t.Fatal(err)
	}
	if said := questions(); said != "" {
		t.Errorf("a run that only reads the state asked the cluster:\n%s", said)
	}

	f.ctx.Converge = true
	if err := Run(f.ctx, "take-over"); err != nil {
		t.Fatal(err)
	}
	said := questions()
	if strings.Count(said, "clusters.postgresql.cnpg.io") != 2 {
		t.Errorf("a converge did not look at its state database twice before applying anything:\n%s", said)
	}
	if strings.Contains(said, "kustomizations") {
		t.Errorf("a converge somebody is watching waited on Flux:\n%s", said)
	}

	// Unattended, it is racing the merge that started it, and waits for Flux
	// to have applied that commit first.
	t.Setenv("GITHUB_ACTIONS", "true")
	if err := Run(f.ctx, "take-over"); err != nil {
		t.Fatal(err)
	}
	said = questions()
	flux, database := strings.Index(said, "get kustomizations"), strings.Index(said, "get clusters")
	if flux < 0 || database < flux {
		t.Errorf("an unattended converge did not ask whether Flux had applied its commit before looking at the database:\n%s", said)
	}
}

// A converge that gave up waiting says what the wait said, and is still
// known for what it is by whoever is handed it: the status a converge ends
// with is decided from it (#713).
func TestAConvergeThatGaveUpWaitingSaysWhyAndIsKnownForWhatItIs(t *testing.T) {
	gaveUp := errors.New("Flux to have applied this commit did not become healthy within 10m0s")
	var handed error = fmt.Errorf("at rest: %w", &NotAtRestError{Err: gaveUp})

	var waiting *NotAtRestError
	if !errors.As(handed, &waiting) {
		t.Fatal("a converge that gave up waiting is not known for it once a phase has wrapped it")
	}
	if waiting.Error() != gaveUp.Error() {
		t.Errorf("it says %q, and what the wait said was %q", waiting.Error(), gaveUp.Error())
	}
	if !errors.Is(handed, gaveUp) {
		t.Error("what the wait said cannot be reached through it")
	}
}
