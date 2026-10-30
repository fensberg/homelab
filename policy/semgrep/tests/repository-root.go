package harness

func root(here string) []string {
	// ruleid: nothing-finds-the-repository-by-counting-directories
	_ = filepath.Join(here, "..", "..")
	// ruleid: nothing-finds-the-repository-by-counting-directories
	_ = filepath.Dir(filepath.Dir(here))
	// ruleid: nothing-finds-the-repository-by-counting-directories
	_ = []string{"..", ".."}
	// ok: nothing-finds-the-repository-by-counting-directories
	_ = filepath.Join(here, "..", "sibling")
	// ok: nothing-finds-the-repository-by-counting-directories
	_ = filepath.Dir(here)
	return nil
}
