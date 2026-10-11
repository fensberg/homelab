package repo

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"homelab/details/applications"
	"homelab/tests/harness"

	"gopkg.in/yaml.v3"
)

// substituted is a "$" that something on a dashboard's way to Grafana would
// take for a variable of its own: Flux takes the braced form, and the lane
// that validates takes a bare name too.
var substituted = regexp.MustCompile(`\$(\{|[A-Za-z_])\w*`)

// dashboards is every dashboard in what Flux applies, by its path. Found by
// what it is - a JSON file holding panels - and not by where it is kept.
func dashboards(t *testing.T) map[string]string {
	t.Helper()
	root := repoRoot(t)
	found := map[string]string{}
	err := filepath.WalkDir(filepath.Join(root, harness.FluxTree), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".json" {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(body), `"panels"`) && strings.Contains(string(body), `"schemaVersion"`) {
			rel, _ := filepath.Rel(root, path)
			found[filepath.ToSlash(rel)] = string(body)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Fatal("no dashboard was found in what Flux applies, so this checked nothing. Criterion 19 of epoch 04 asks for one for each question the epoch asks.")
	}
	return found
}

// A dashboard is kept in git, reaches Grafana, and says what each picture is
// for.
//
// Three ways one fails without anything saying so. It holds a variable: Flux
// substitutes into everything it applies, and the lane that validates
// substitutes a bare "$name" too, so a dashboard's own variable is blanked
// on the way in and its panels are empty for no reason anybody can see. It
// is written and never handed over: a file nothing lists is a dashboard that
// is not there. And it is a picture with no account of itself, which is how
// monitoring becomes decoration - this epoch's own words for it.
func TestEveryDashboardReachesGrafanaAndSaysWhatItIsFor(t *testing.T) {
	root := repoRoot(t)
	var paths []string
	all := dashboards(t)
	for path := range all {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		body := all[path]
		// A "$" before a digit is a part of a pattern, which nothing
		// substitutes: the rules beside the dashboards use it the same way.
		if m := substituted.FindString(body); m != "" {
			t.Errorf("%s holds %q. What Flux applies is substituted into on its way in, so a dashboard's own variable arrives blank. Write the panel with no variable.", path, m)
		}
		var d map[string]any
		if err := json.Unmarshal([]byte(body), &d); err != nil {
			t.Errorf("%s is not JSON a dashboard can be read from: %v", path, err)
			continue
		}
		text := func(of map[string]any, key string) string {
			s, _ := of[key].(string)
			return strings.TrimSpace(s)
		}
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		if uid := text(d, "uid"); uid != name {
			t.Errorf("%s has the uid %q. A dashboard is linked to by its uid, so it is the file's own name and does not change when the title does.", path, uid)
		}
		panels, _ := d["panels"].([]any)
		if len(panels) == 0 {
			t.Errorf("%s has no panel", path)
		}
		for _, each := range panels {
			p, _ := each.(map[string]any)
			asks := false
			targets, _ := p["targets"].([]any)
			for _, target := range targets {
				one, _ := target.(map[string]any)
				asks = asks || text(one, "expr") != ""
			}
			switch {
			case text(p, "title") == "":
				t.Errorf("%s has a panel with no title", path)
			case text(p, "description") == "":
				t.Errorf("%s: the panel %q does not say what it is for. A picture with no account of what reading means what is one nobody acts on.", path, text(p, "title"))
			case !asks:
				t.Errorf("%s: the panel %q asks Prometheus nothing", path, text(p, "title"))
			}
		}

		// Handed to Grafana: listed by the kustomization beside its
		// directory, under the label the sidecar watches for.
		listing := beside(filepath.Dir(path), applications.Kustomization)
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(listing)))
		if err != nil {
			t.Errorf("%s is not handed to Grafana: no kustomization is beside its directory to list it (%v)", path, err)
			continue
		}
		var k struct {
			Generators []struct {
				Files []string `yaml:"files"`
			} `yaml:"configMapGenerator"`
			Options struct {
				Labels map[string]string `yaml:"labels"`
			} `yaml:"generatorOptions"`
		}
		if err := yaml.Unmarshal(raw, &k); err != nil {
			t.Fatalf("parsing %s: %v", listing, err)
		}
		listed := false
		want := filepath.ToSlash(filepath.Join(filepath.Base(filepath.Dir(path)), filepath.Base(path)))
		for _, g := range k.Generators {
			for _, f := range g.Files {
				listed = listed || f == want
			}
		}
		if !listed {
			t.Errorf("%s is not handed to Grafana: the kustomization beside its directory does not list it, so it is a file and not a dashboard.", path)
		}
		if k.Options.Labels["grafana_dashboard"] == "" {
			t.Errorf("%s is listed and not labelled grafana_dashboard, so the sidecar that hands dashboards to Grafana does not see it.", path)
		}
	}
}
