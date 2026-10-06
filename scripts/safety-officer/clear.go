package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"homelab/details/applications"
	"homelab/details/cloudflare"
	"homelab/details/holds"
	"homelab/details/onepassword"
	"homelab/details/vaults"
)

// held is one asset the site holds, and the bucket its copy is in.
type held struct {
	holds.Asset
	storage string
}

// clear answers the question and prints the answer. It is true only when
// everything the destruction would take is either not worth more than what
// is being destroyed, or has a copy outside it that was looked at and is
// young enough. Anything that could not be found out is a refusal.
func clear(out io.Writer, repoRoot, site, destroying string, now time.Time) bool {
	scope, ok := holds.ScopeNamed(destroying)
	if !ok || scope != holds.Site {
		fmt.Fprintf(out, "REFUSED: asked about destroying a %q, and a site is the only thing this can clear yet.\n", destroying)
		return false
	}
	assets, err := heldBy(repoRoot, site)
	if err != nil {
		fmt.Fprintf(out, "REFUSED: what %s holds cannot be made out, so nothing can be said about losing it.\n\n  %v\n", site, err)
		return false
	}

	var lost []string
	for _, a := range assets {
		if !a.EndangeredBy(scope) {
			continue
		}
		problem := a.unsafe(site, now)
		if problem == "" {
			fmt.Fprintf(out, "  [ok] %s (%s) has a copy young enough to lose nothing that matters\n", a.What, a.Owner)
			continue
		}
		lost = append(lost, fmt.Sprintf("  %s (%s)\n    worth keeping for: the life of %s\n    %s", a.What, a.Owner, lifetimeWords(a.Lifetime), problem))
	}
	if len(lost) == 0 {
		fmt.Fprintf(out, "  [ok] destroying %s loses nothing that should outlive it\n", site)
		return true
	}
	fmt.Fprintf(out, `REFUSED: destroying %s would lose something that should outlive it.

%s

The officer changed nothing. Make a fresh copy, or stop the site holding it, and
ask again. There is no flag for this.
`, site, strings.Join(lost, "\n\n"))
	return false
}

func lifetimeWords(lifetime string) string {
	if lifetime == holds.Client.String() {
		return "whoever it belongs to, which is longer than the estate's"
	}
	return "the " + lifetime
}

// heldBy is everything the site holds: the core's assets, and those of each
// application the site was given, with the bucket for the environment it
// runs that application in.
func heldBy(repoRoot, site string) ([]held, error) {
	core, err := holds.ReadCore(repoRoot)
	if err != nil {
		return nil, err
	}
	var out []held
	for _, a := range core {
		storage := ""
		if a.Copy != nil {
			storage = a.Copy.Storage
		}
		out = append(out, held{a, storage})
	}
	apps, err := applications.Read(repoRoot)
	if err != nil {
		return nil, err
	}
	assigned, err := applications.Assigned(repoRoot, site)
	if err != nil {
		return nil, err
	}
	for _, given := range assigned {
		found := false
		for _, app := range apps {
			if app.Name != given.Application {
				continue
			}
			found = true
			for _, a := range app.Holds {
				out = append(out, held{a, given.Environment})
			}
		}
		if !found {
			return nil, fmt.Errorf("%s is given %s, and there is no such application to ask what it holds", site, given.Application)
		}
	}
	return out, nil
}

// unsafe says why destroying the asset's home would lose it, or "" when a
// copy elsewhere was looked at and is young enough.
func (a held) unsafe(site string, now time.Time) string {
	if a.Copy == nil {
		return "copy:              none anywhere else"
	}
	tolerance, err := a.Tolerance()
	if err != nil {
		return "copy:              " + err.Error()
	}
	newest, found, err := newestCopy(site, a.storage, a.Copy.Under)
	switch {
	case err != nil:
		return fmt.Sprintf("copy:              could not be looked at in the %s bucket (%v)", a.storage, err)
	case !found:
		return fmt.Sprintf("copy:              none in the %s bucket", a.storage)
	}
	if age := now.Sub(newest); age > tolerance {
		return fmt.Sprintf("newest copy:       %s old, in the %s bucket\n    may lose:          %s", span(age), a.storage, span(tolerance))
	}
	return ""
}

// The site's item in its shared vault that the estate grants its buckets
// into, and the remote rclone is told about for one listing.
const (
	storageItem = vaults.ObjectStorage
	remote      = "COPIES"
)

// newestCopy is when the newest object under a folder of one of the site's
// buckets was put there, as the storage itself says - not the time the file
// claims for itself, which is when it was last changed at the source and
// says nothing about when the copy was made.
//
// It is read with the bucket's read-only key. What rclone prints on a
// failure is not passed on, because it names the bucket.
func newestCopy(site, storage, under string) (newest time.Time, found bool, err error) {
	field := func(name string) (string, error) {
		return onepassword.Read(fmt.Sprintf("op://%s/%s/%s", vaults.SiteShared(site), storageItem, name))
	}
	var account, bucket, keyID, secret string
	for _, want := range []struct {
		name string
		into *string
	}{
		{"account_id", &account},
		{storage + "_bucket", &bucket},
		{storage + "_reader_access_key_id", &keyID},
		{storage + "_reader_secret_access_key", &secret},
	} {
		name, into := want.name, want.into
		v, err := field(name)
		if err != nil || v == "" {
			return newest, false, fmt.Errorf("the site's vault does not hold %s; the estate grants it, with the lawyer's converge-estate", name)
		}
		*into = v
	}
	cmd := exec.Command("rclone", "lsjson", "--recursive", "--files-only", "--no-mimetype", "--use-server-modtime",
		remote+":"+bucket+"/"+strings.Trim(under, "/"))
	cmd.Env = append(os.Environ(), cloudflare.RcloneEnv(remote, account, keyID, secret)...)
	listed, err := cmd.Output()
	if err != nil {
		return newest, false, fmt.Errorf("rclone could not list it: %w", err)
	}
	var objects []struct {
		// As rclone spells it; the decoder matches the field by name.
		ModTime time.Time
	}
	if err := json.Unmarshal(listed, &objects); err != nil {
		return newest, false, fmt.Errorf("rclone's listing was not one: %w", err)
	}
	for _, o := range objects {
		if o.ModTime.After(newest) {
			newest, found = o.ModTime, true
		}
	}
	return newest, found, nil
}

// span is a length of time in the largest unit that says it.
func span(d time.Duration) string {
	plural := func(n int, unit string) string {
		if n == 1 {
			return fmt.Sprintf("1 %s", unit)
		}
		return fmt.Sprintf("%d %ss", n, unit)
	}
	switch {
	case d >= 48*time.Hour:
		return plural(int(d/(24*time.Hour)), "day")
	case d >= 2*time.Hour:
		return plural(int(d/time.Hour), "hour")
	default:
		return plural(int(d/time.Minute), "minute")
	}
}
