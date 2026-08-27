package phases

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"

	"homelab/ignite/internal/run"
)

// How many timestamped generations survive a prune, newest first.
// latest.tfstate.age is a pointer, overwritten every run, and is not counted -
// so the bucket holds this many generations plus that one object, forever.
//
// Two rather than one because the failure this guards against is a backup
// that uploaded successfully and is nonetheless unusable. One generation means
// discovering that with nothing to fall back to.
const keepGenerations = 2

// Generation objects are named by the same timestamp layout backup.go writes:
// 20060102-150405.tfstate.age.
var generationName = regexp.MustCompile(`^(\d{8}-\d{6})\.tfstate\.age$`)

// pruneTargets decides which objects to delete, and refuses to decide at all
// if the upload that was just made is not in the listing.
//
// That refusal is the whole point. Deleting the previous generation is only
// safe once the new one is known to have landed, and "the upload command
// exited zero" is not that - it is a claim about a request, not about what is
// in the bucket. This is given the result of a fresh listing so the decision
// is made against what is actually there.
func pruneTargets(objects []string, newStamp string, keep int) ([]string, error) {
	if keep < 1 {
		return nil, fmt.Errorf("keep must be at least 1, got %d; pruning every generation is never correct", keep)
	}

	var generations []string
	for _, o := range objects {
		if generationName.MatchString(o) {
			generations = append(generations, o)
		}
	}

	// The gate.
	want := newStamp + ".tfstate.age"
	if !slices.Contains(generations, want) {
		return nil, fmt.Errorf(`the backup just uploaded (%s) is not in the bucket listing, so nothing will be deleted.

The upload reported success and the object is not there. Until that is
understood, the previous generations are the only copies that exist and they
are being left alone`, want)
	}

	// Timestamps in this layout sort lexicographically, so this is
	// newest-last without parsing anything.
	slices.Sort(generations)
	if len(generations) <= keep {
		return nil, nil
	}
	return generations[:len(generations)-keep], nil
}

// rcloneList returns the object names directly under dest.
func rcloneList(ctx *run.Context, env []string, dest string) ([]string, error) {
	out, err := run.CmdOutputEnv(ctx.ClusterDir, env, "rclone", "lsjson", dest)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", dest, err)
	}
	var entries []struct {
		Path  string `json:"Path"`
		Size  int64  `json:"Size"`
		IsDir bool   `json:"IsDir"`
	}
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		return nil, fmt.Errorf("parsing the listing of %s: %w", dest, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir {
			names = append(names, e.Path)
		}
	}
	return names, nil
}

// pruneOldBackups lists what is actually in the bucket, confirms the upload
// just made is there, and deletes everything past keepGenerations.
//
// A failure here is reported and does not fail the phase. The backup itself
// has already succeeded at this point; refusing to finish because some old
// object could not be deleted would turn a storage-tidiness problem into a
// failed ignition run.
func pruneOldBackups(ctx *run.Context, env []string, dest, newStamp string) {
	objects, err := rcloneList(ctx, env, dest)
	if err != nil {
		run.Warn("could not list the backup bucket, so nothing was pruned: " + err.Error())
		return
	}

	targets, err := pruneTargets(objects, newStamp, keepGenerations)
	if err != nil {
		run.Warn(err.Error())
		return
	}
	if len(targets) == 0 {
		run.Info(fmt.Sprintf("%d generation(s) stored, keeping %d - nothing to prune", countGenerations(objects), keepGenerations))
		return
	}

	for _, t := range targets {
		run.Info("pruning " + t)
		if err := run.CmdEnv(ctx.ClusterDir, env, "rclone", "deletefile", dest+"/"+t); err != nil {
			run.Warn("could not delete " + t + ": " + err.Error())
		}
	}
	run.Ok(fmt.Sprintf("pruned %d old generation(s); %d kept", len(targets), keepGenerations))
}

func countGenerations(objects []string) int {
	n := 0
	for _, o := range objects {
		if generationName.MatchString(o) {
			n++
		}
	}
	return n
}
