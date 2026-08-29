//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"homelab/tests/harness"
)

// The alarm.
//
// Nothing here is monitored by a human, and a backup that quietly stopped six
// weeks ago is indistinguishable from a healthy one until the day it matters.
// So the nightly workflow takes a fresh backup first and then runs these
// assertions against what actually landed. That makes the whole path -
// pulling state, encrypting it, uploading it, pruning the old generation -
// exercised every night rather than only during an ignition run, and it makes
// "how old is the newest backup" a question with a meaningful answer.
//
// The alert is the workflow failing. GitHub already emails on that, which is
// one fewer moving part than any monitoring stack would be.

// The nightly job takes a backup immediately before this runs, so anything
// older than a day means that step did not do what it claimed.
const backupFreshness = 25 * time.Hour

// Every age file begins with this. Checking it proves the object is a
// well-formed age header rather than a truncated or empty upload - which is
// the most that can be checked without the identity, and the identity is
// deliberately offline. See docs/state-and-secret-rotation.md.
const ageMagic = "age-encryption.org/v1"

type r2Object struct {
	Path    string    `json:"Path"`
	Size    int64     `json:"Size"`
	ModTime time.Time `json:"ModTime"`
	IsDir   bool      `json:"IsDir"`
}

func rcloneEnv(t *testing.T) []string {
	t.Helper()
	site := harness.SiteConfig(t)
	require.NotEmpty(t, site.ObjectStorage.AccountID, "the rendered config has no object_storage.account_id")
	return []string{
		"RCLONE_CONFIG_R2_TYPE=s3",
		"RCLONE_CONFIG_R2_PROVIDER=Cloudflare",
		"RCLONE_CONFIG_R2_ACCESS_KEY_ID=" + site.ObjectStorage.AccessKeyID,
		"RCLONE_CONFIG_R2_SECRET_ACCESS_KEY=" + site.ObjectStorage.SecretAccessKey,
		"RCLONE_CONFIG_R2_ENDPOINT=https://" + site.ObjectStorage.AccountID + ".r2.cloudflarestorage.com",
		"RCLONE_CONFIG_R2_NO_CHECK_BUCKET=true",
	}
}

func listBackups(t *testing.T) []r2Object {
	t.Helper()
	site := harness.SiteConfig(t)
	dest := fmt.Sprintf("R2:%s/management-cluster", site.ObjectStorage.Bucket)

	out, err := harness.RunEnv(t, rcloneEnv(t), "rclone", "lsjson", dest)
	require.NoErrorf(t, err, "listing %s - if this is an auth failure the R2 credentials in the vault no longer work, which means backups have been failing too", dest)

	var objs []r2Object
	require.NoError(t, json.Unmarshal([]byte(out), &objs), "parsing the bucket listing")
	return objs
}

func TestNewestBackupIsFreshAndWellFormed(t *testing.T) {
	objs := listBackups(t)

	var latest *r2Object
	for i := range objs {
		if objs[i].Path == "latest.tfstate.age" {
			latest = &objs[i]
		}
	}
	require.NotNil(t, latest, `there is no latest.tfstate.age in the bucket.

The nightly job runs the Backup phase immediately before this test, so either
that step failed silently or something deleted the object.`)

	age := time.Since(latest.ModTime)
	assert.Lessf(t, age, backupFreshness,
		"the newest backup is %s old. A backup was supposed to have been taken minutes ago, so the Backup phase is failing without failing loudly.", age.Round(time.Hour))

	// A few hundred bytes would be an empty or truncated state.
	assert.Greaterf(t, latest.Size, int64(1024),
		"latest.tfstate.age is only %d bytes, which is too small to be a real encrypted state file", latest.Size)

	site := harness.SiteConfig(t)
	head, err := harness.RunEnv(t, rcloneEnv(t), "rclone", "cat", "--count", "64",
		fmt.Sprintf("R2:%s/management-cluster/latest.tfstate.age", site.ObjectStorage.Bucket))
	require.NoError(t, err, "reading the first bytes of the newest backup")
	assert.Containsf(t, head, ageMagic,
		"latest.tfstate.age does not start with an age header, so it is not a well-formed encrypted file. Decrypting it to check further is deliberately impossible from here - the identity is offline.")
}

// Storage must stay flat. If this grows, the prune in the Backup phase has
// stopped running or is refusing to act - which it does, on purpose, whenever
// it cannot confirm the new upload landed.
func TestBackupGenerationsAreBounded(t *testing.T) {
	objs := listBackups(t)

	var generations []string
	for _, o := range objs {
		if o.IsDir || o.Path == "latest.tfstate.age" {
			continue
		}
		if strings.HasSuffix(o.Path, ".tfstate.age") {
			generations = append(generations, o.Path)
		}
	}
	sort.Strings(generations)

	// keepGenerations in scripts/ignite/internal/phases/prune.go.
	const keep = 2
	assert.LessOrEqualf(t, len(generations), keep,
		"%d timestamped generations are stored, expected at most %d: %v\n\nThe prune is not running, or is refusing to delete because it cannot confirm the newest upload landed - check the Backup phase output for a warning.",
		len(generations), keep, generations)
}

// WAL archiving is what makes point-in-time recovery possible at all, and it
// can fail continuously without anything else noticing. pg_stat_archiver is
// plain Postgres, so these column names are stable in a way CloudNativePG's
// own status fields are not.
func TestWALArchivingIsHealthy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, harness.StateConnString(t))
	require.NoError(t, err, "connecting to the state database over the NodePort")
	defer conn.Close(ctx)

	var archived, failed int64
	var lastArchived, lastFailed *time.Time
	err = conn.QueryRow(ctx, `
		SELECT archived_count, last_archived_time, failed_count, last_failed_time
		FROM pg_stat_archiver
	`).Scan(&archived, &lastArchived, &failed, &lastFailed)
	require.NoError(t, err, "querying pg_stat_archiver")

	require.Greaterf(t, archived, int64(0),
		"no WAL segment has ever been archived. Point-in-time recovery does not work at all - check the object-storage credentials in the %s namespace.", "database")

	if lastFailed != nil && lastArchived != nil {
		assert.Truef(t, lastArchived.After(*lastFailed),
			"the last WAL archive attempt failed (%s) more recently than the last success (%s). Archiving is currently broken; %d failures so far.",
			lastFailed.Format(time.RFC3339), lastArchived.Format(time.RFC3339), failed)
	}
}
