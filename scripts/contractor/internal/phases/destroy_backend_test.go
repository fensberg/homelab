package phases

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"homelab/contractor/internal/run"
)

// A failed attach must not leave a backend configuration behind.
//
// WHAT THIS GUARDS. When there is no local state, destroy assumes state is
// where a successful ignition puts it and writes backend_pg.tf to go and look.
// If that init fails - the usual reason being that the cluster is already gone -
// the file has to come back out.
//
// Leaving it is not a cosmetic untidiness. backend_pg.tf declares a Postgres
// backend for the whole module, so EVERY later `tofu init` in that workspace
// picks it up, including phases that have nothing to do with cluster state.
// With no -backend-config to go with it, tofu dials localhost:
//
//	PHASE 2 : OVERLAY
//	Error: dial tcp [::1]:5432: connect: connection refused
//
// So a teardown that could not find a cluster to tear down leaves the
// workspace unable to mint a tailnet key, and the next ignition dies in its
// second phase with an error naming Postgres, which is not involved. The
// estate deadlocks: the destroy cannot proceed and the rebuild cannot start.
func TestAFailedPostgresAttachRemovesTheBackendFile(t *testing.T) {
	dir := t.TempDir()
	ctx := &run.Context{
		BackendPgOff: filepath.Join(dir, "backend_pg.tf.disabled"),
		BackendPgOn:  filepath.Join(dir, "backend_pg.tf"),
	}
	if err := os.WriteFile(ctx.BackendPgOff, []byte("terraform {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	wantErr := errors.New("connect: no route to host")
	err := attachToStateInPostgres(ctx, func() error { return wantErr })

	if !errors.Is(err, wantErr) {
		t.Fatalf("got %v, want the init error wrapped", err)
	}
	if _, statErr := os.Stat(ctx.BackendPgOn); !os.IsNotExist(statErr) {
		t.Fatalf(`backend_pg.tf is still present after a failed attach.

Every later tofu init in this workspace will now use a Postgres backend with no
connection string and dial localhost, so the next ignition fails in its Overlay
phase with an error about Postgres, which is not involved.`)
	}
}

// The success path must leave it in place - that is the whole point of writing
// it, and a cleanup that fired unconditionally would break the destroy it is
// meant to enable.
func TestASuccessfulPostgresAttachKeepsTheBackendFile(t *testing.T) {
	dir := t.TempDir()
	ctx := &run.Context{
		BackendPgOff: filepath.Join(dir, "backend_pg.tf.disabled"),
		BackendPgOn:  filepath.Join(dir, "backend_pg.tf"),
	}
	if err := os.WriteFile(ctx.BackendPgOff, []byte("terraform {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := attachToStateInPostgres(ctx, func() error { return nil }); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
	if _, err := os.Stat(ctx.BackendPgOn); err != nil {
		t.Fatalf("backend_pg.tf was removed after a successful attach: %v", err)
	}
}
