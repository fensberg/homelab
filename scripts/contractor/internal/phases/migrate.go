package phases

import (
	"fmt"
	"homelab/details/tcp"
	"io"
	"os"
	"regexp"
	"strconv"
	"time"

	"homelab/contractor/internal/run"
)

var connStrHostPort = regexp.MustCompile(`@([^:/@]+):(\d+)`)

// Migrate moves OpenTofu state off this disk and into cluster Postgres.
func Migrate(ctx *run.Context) error {
	run.WritePhase("Migrate", "Move OpenTofu state off this disk and into cluster Postgres.")

	// Derived from variables.tf plus the 1Password password, not stored as
	// a secret of its own - see the state_conn_str output in database.tf.
	// Storing it would invent a chicken-and-egg problem: you cannot record
	// a connection string for a database that does not exist yet.
	// The platform root declares the database, so the connection string is
	// its output.
	connStr, err := run.TofuOutputRaw(ctx.In(ctx.Platform), "state_conn_str")
	if err != nil {
		return fmt.Errorf("could not read the state_conn_str output. Has the Cluster phase run? (%w)", err)
	}

	m := connStrHostPort.FindStringSubmatch(connStr)
	if m == nil {
		return fmt.Errorf("could not parse a host and port out of the derived connection string")
	}
	pgHost := m[1]
	pgPort, _ := strconv.Atoi(m[2])

	run.Info(fmt.Sprintf("waiting for Postgres at %s:%d ...", pgHost, pgPort))
	if !tcp.Await(tcp.Addr(pgHost, pgPort), 10*time.Minute, 15*time.Second) {
		return fmt.Errorf("postgres at %s:%d never became reachable. Has Flux finished reconciling it?", pgHost, pgPort)
	}
	run.Ok("Postgres reachable")

	// Turn the backend on by copying the file in. It stays '.disabled' in
	// git so a fresh clone always starts on local state.
	// Each root into its own schema. The cluster root first: it is the one
	// a teardown needs, so if this stops between the two, the state that
	// matters is already where a later run looks for it.
	for _, root := range ctx.Roots() {
		run.Info("enabling the Postgres backend for the " + root.Name + " root")
		if err := copyFile(root.BackendPgOff, root.BackendPgOn); err != nil {
			return err
		}

		run.Info("migrating the " + root.Name + " root's state (local -> Postgres)")
		if err := run.Tofu(ctx.In(root), "tofu init -migrate-state",
			"init", "-input=false", "-migrate-state", "-force-copy",
			"-backend-config=conn_str="+connStr,
		); err != nil {
			return err
		}
	}

	run.Ok("state now lives in Postgres")
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
