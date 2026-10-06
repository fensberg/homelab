package repo

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// An image that installs whatever version of a package the archive has starts
// from a base pinned by digest.
//
// hadolint's DL3008 wants every apt package pinned to an exact version, and
// each place it is silenced says why not: Debian's and Ubuntu's archives keep
// only the current version, so a pin stops existing the day a security update
// supersedes it, and until then it holds the image on the version that is not
// getting the update.
//
// That leaves the packages unpinned, which is only tolerable if it is all
// that is. So this holds what the comments rest on: the stage that runs
// `apt-get install` starts FROM an image named by its digest. What can then
// differ between two builds of one Dockerfile is the distribution's own
// updates to packages named here, on top of contents that cannot move - and
// not a base that was swapped under a tag.
func TestAnImageThatInstallsUnpinnedPackagesStartsFromAPinnedBase(t *testing.T) {
	from := regexp.MustCompile(`(?i)^FROM\s+(\S+)`)
	installs := regexp.MustCompile(`\bapt(-get)?\s+install\b`)

	dockerfiles, checked := 0, 0
	for _, rel := range trackedFiles(t) {
		if !strings.HasPrefix(filepath.Base(rel), "Dockerfile") {
			continue
		}
		dockerfiles++
		base, baseLine := "", 0
		for i, line := range strings.Split(readRepoFile(t, rel), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			if m := from.FindStringSubmatch(trimmed); m != nil {
				base, baseLine = m[1], i+1
				continue
			}
			if !installs.MatchString(trimmed) {
				continue
			}
			checked++
			if !strings.Contains(base, "@sha256:") {
				t.Errorf("%s:%d installs packages at whatever version the archive has, in a stage that starts from %q (line %d), which is not pinned by digest.\n\n"+
					"Unpinned packages are tolerable on a base that cannot move. On a tag, both move, and two builds of this file share nothing that is held.",
					rel, i+1, base, baseLine)
			}
		}
	}
	if dockerfiles == 0 || checked == 0 {
		t.Fatalf("found %d Dockerfile(s) and %d package install(s), so nothing was checked", dockerfiles, checked)
	}
}
