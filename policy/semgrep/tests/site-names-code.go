package a

// ok: no-program-picks-a-site-by-name
// the first site, site0
var (
	// ruleid: no-program-picks-a-site-by-name
	s = "site0"
	// ruleid: no-program-picks-a-site-by-name
	u = `run: contractor plan -site site0`
	// ok: no-program-picks-a-site-by-name
	v = "site10"
)
