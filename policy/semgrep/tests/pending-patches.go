package repo

import "path/filepath"

func readsPatches(root string) []string {
	return []string{
		// ruleid: no-test-reads-the-patches-directory
		filepath.Join(root, ".github", "patches"),
		// ruleid: no-test-reads-the-patches-directory
		root + "/.github/patches/lanes.patch",
		// ok: no-test-reads-the-patches-directory
		filepath.Join(root, ".github", "workflows"),
	}
}

// ok: no-test-reads-the-patches-directory
// the operator applies .github/patches with task apply-patches
