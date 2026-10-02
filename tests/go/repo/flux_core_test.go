package repo

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"homelab/contractor/config"
	"homelab/details/applications"
)

// The Flux tree is a core every site runs, and a directory for each site
// that has been given work.
//
// WHY THIS EXISTS. Every site reconciled one directory, so anything in it that
// was one site's - the runners' name, the workloads it runs - was every
// site's the day a second existed. The tree is now split by who it is for:
// clusters/core is what makes a cluster a site of this estate, and
// clusters/<site>/ is what that site runs on top, built on the core.
//
// Three things keep that true, and each is checked of whatever is there
// rather than of a list:
//
//   - every directory in the tree is the core, a declared site's, or the
//     CNI's bootstrap, so a directory nobody can place is refused;
//   - a site's directory builds on the core, so a site never runs its work
//     without the core beneath it;
//   - the core names no site and assigns no work, so bringing a site online
//     needs nothing written for it, and giving it work is a commit to its
//     own directory.
func TestTheFluxCoreIsSharedAndASiteDirectoryIsItsWork(t *testing.T) {
	files := map[string]string{}
	for _, rel := range tracked(t, func(rel string) bool { return strings.HasPrefix(rel, fluxTree+"/") }) {
		files[rel] = readRepoFile(t, rel)
	}
	sites := declaredSites(t)

	// Found by what they hold, not named: the core is where Flux's own
	// install is, and the bootstrap is where the CNI's manifest is.
	install, err := fluxObjectPath(fluxInstallKind, fluxInstallName)
	if err != nil {
		t.Fatal(err)
	}
	cni, err := fluxObjectPath(cniKind, cniName)
	if err != nil {
		t.Fatal(err)
	}
	layout := fluxLayout{
		core:      topOf(install),
		bootstrap: topOf(cni),
		vendored:  filepath.ToSlash(filepath.Dir(install)) + "/",
		sites:     sites,
	}
	problems, err := layout.problems(files)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

// declaredSites is the key of every site the config template declares,
// decoded with the config's own types.
func declaredSites(t *testing.T) []string {
	t.Helper()
	var tpl config.Config
	if err := json.Unmarshal([]byte(readRepoFile(t, "config/management.tpl.json")), &tpl); err != nil {
		t.Fatalf("config/management.tpl.json: %v", err)
	}
	sites := make([]string, 0, len(tpl.Sites))
	for site := range tpl.Sites {
		sites = append(sites, site)
	}
	sort.Strings(sites)
	if len(sites) == 0 {
		t.Fatal("the config template declares no site, so nothing here could be held to one")
	}
	return sites
}

// topOf is the directory directly under the Flux tree that a path is in.
func topOf(rel string) string {
	parts := strings.Split(rel, "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

// fluxLayout is who each part of the Flux tree is for.
type fluxLayout struct {
	core, bootstrap string
	// vendored is the directory holding Flux's own install, which is
	// generated and held to none of this.
	vendored string
	sites    []string
}

var workPath = regexp.MustCompile(`(?m)^\s*path:\s*\.?/?` + applications.Dir + `/`)

func (l fluxLayout) problems(files map[string]string) ([]string, error) {
	var out []string
	isSite := map[string]bool{}
	for _, s := range l.sites {
		isSite[s] = true
	}
	dirs := map[string]bool{}
	for rel := range files {
		dirs[topOf(rel)] = true
	}
	for dir := range dirs {
		switch {
		case dir == l.core, dir == l.bootstrap:
		case isSite[dir]:
			entry, ok := files[fluxTree+"/"+dir+"/kustomization.yaml"]
			if !ok {
				out = append(out, fmt.Sprintf("%s/%s has no kustomization.yaml. Flux would generate one that flattens in everything beneath it, and the platform root only points a site's Flux at a directory that has one.", fluxTree, dir))
				continue
			}
			var k struct {
				Resources []string `yaml:"resources"`
			}
			if err := yaml.Unmarshal([]byte(entry), &k); err != nil {
				return nil, fmt.Errorf("%s/%s/kustomization.yaml does not parse, so what it builds on cannot be read: %w", fluxTree, dir, err)
			}
			builds := false
			for _, r := range k.Resources {
				if filepath.ToSlash(filepath.Clean(r)) == "../"+l.core {
					builds = true
				}
			}
			if !builds {
				out = append(out, fmt.Sprintf("%s/%s/kustomization.yaml does not build on ../%s. A site's Flux is pointed at its own directory, so a site whose directory leaves the core out runs its work with nothing beneath it - no storage, no database, no runners.", fluxTree, dir, l.core))
			}
		default:
			out = append(out, fmt.Sprintf("%s/%s is not the core, the CNI's bootstrap, or the directory of a site the config declares (%s). Nothing reconciles it, or a site that does not exist has been given work.", fluxTree, dir, strings.Join(l.sites, ", ")))
		}
	}

	for rel, body := range files {
		if topOf(rel) != l.core || strings.HasPrefix(rel, l.vendored) && filepath.Base(rel) != applications.Kustomization && filepath.Base(rel) != "gotk-sync.yaml" {
			continue
		}
		code := stripYAMLComments(body)
		for _, site := range l.sites {
			if regexp.MustCompile(`(^|[^A-Za-z0-9_])` + regexp.QuoteMeta(site) + `($|[^A-Za-z0-9_])`).MatchString(code) {
				out = append(out, fmt.Sprintf("%s names %s. The core is what every site runs, written once: what differs by site is a variable Flux fills in from the site's own cluster-vars (${SITE}), or belongs in %s/%s/.", rel, site, fluxTree, site))
			}
		}
		if workPath.MatchString(code) {
			out = append(out, fmt.Sprintf("%s points Flux at a workload. The core is what a site needs to be a site, and a build waits on nothing else; work is assigned in a site's own directory.", rel))
		}
	}
	sort.Strings(out)
	return out, nil
}

// stripYAMLComments drops whole-line comments, so prose about a site or a
// workload is not read as naming one.
func stripYAMLComments(body string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n")
}

func TestFluxLayoutRefusesWhatIsNotCoreOrASitesOwn(t *testing.T) {
	l := fluxLayout{core: "shared", bootstrap: "first", vendored: fluxTree + "/shared/engine/", sites: []string{"alpha", "beta"}}
	good := map[string]string{
		fluxTree + "/shared/kustomization.yaml":    "resources: [engine, parts.yaml]\n",
		fluxTree + "/shared/parts.yaml":            "# alpha was the first site\nname: runners-${SITE}\npath: ./" + fluxTree + "/shared/parts\n",
		fluxTree + "/shared/engine/generated.yaml": "name: alpha-is-a-word-upstream-uses\n",
		fluxTree + "/first/rendered.yaml":          "kind: DaemonSet\n",
		fluxTree + "/alpha/kustomization.yaml":     "resources:\n  - ../shared\n  - work.yaml\n",
		fluxTree + "/alpha/work.yaml":              "path: ./" + applications.Dir + "/x/production\n",
	}
	if got, err := l.problems(good); err != nil || len(got) != 0 {
		t.Fatalf("a core and one site's work were refused: %v, %v", got, err)
	}
	for name, tc := range map[string]struct{ file, body, want string }{
		"a directory nobody can place":     {fluxTree + "/gamma/kustomization.yaml", "resources: [../shared]\n", "is not the core"},
		"a site's directory with no entry": {fluxTree + "/beta/work.yaml", "path: ./x\n", "has no kustomization.yaml"},
		"a site that leaves the core out":  {fluxTree + "/beta/kustomization.yaml", "resources: [work.yaml]\n", "does not build on ../shared"},
		"the core naming a site":           {fluxTree + "/shared/extra.yaml", "name: runners-alpha\n", "names alpha"},
		"the core's sync naming a site":    {fluxTree + "/shared/engine/gotk-sync.yaml", "path: ./" + fluxTree + "/beta\n", "names beta"},
		"the core assigning work":          {fluxTree + "/shared/extra.yaml", "  path: ./" + applications.Dir + "/x/production\n", "points Flux at a workload"},
	} {
		bad := map[string]string{tc.file: tc.body}
		for k, v := range good {
			if _, replaced := bad[k]; !replaced {
				bad[k] = v
			}
		}
		got, err := l.problems(bad)
		if err != nil || len(got) != 1 || !strings.Contains(got[0], tc.want) {
			t.Errorf("%s: got %v (%v), want one problem saying %q", name, got, err, tc.want)
		}
	}
	unreadable := map[string]string{fluxTree + "/alpha/kustomization.yaml": "resources: [unclosed\n"}
	if _, err := l.problems(unreadable); err == nil {
		t.Error("a site's entry that does not parse was read as building on nothing, rather than refused")
	}
}
