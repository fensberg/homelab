package repo

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"homelab/details/applications"
	"homelab/details/flux"
)

// A site's applications file holds one block per application, each held to
// what that application declares.
//
// A site is given an application by a block in its own file: the release it
// runs, and the Kustomization that reconciles one environment's settings from
// it. Nothing else says a site runs an application, so the block is where
// every rule about running one has to hold - and each is a rule the cluster
// then enforces, rather than one a reviewer has to remember:
//
//   - it reconciles a directory of the application's own, for an environment
//     the application has settings for;
//   - it reconciles as the application's own identity, into the application's
//     own namespace. The platform binds that identity to that namespace and
//     nothing else, so a manifest reaching outside it is refused by the API
//     server;
//   - its release is the application's own, by name;
//   - it waits for every application its own requires, which this site must
//     therefore run too. That is the one way an application may depend on
//     another, and it is declared.
//
// And the dependency-free reading the contractor makes of the same file
// (applications.ParseAssigned) must see exactly the applications this does.
//
// Found by walking the sites, so the next site's file is held to it the day
// it is written; an estate where no site runs anything has none, and what
// this refuses is proved against files written here.
func TestEverySiteRunsItsApplicationsAsTheyAreDeclared(t *testing.T) {
	root := repoRoot(t)
	apps, err := applications.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	environments := map[string][]string{}
	for _, a := range apps {
		if environments[a.Name], err = a.Environments(root); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range tracked(t, func(rel string) bool {
		parts := strings.Split(rel, "/")
		return len(parts) == 3 && parts[0] == applications.SitesDir && parts[2] == applications.SiteFile
	}) {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		for _, problem := range siteBlockProblems(rel, string(body), apps, environments) {
			t.Error(problem)
		}
	}
}

// The identity a site reconciles an application as, which the platform
// creates under this name (modules/infrastructure/platform).
const applicationReconcilerPrefix = "application-"

type siteDocument struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Spec struct {
		URL       string `yaml:"url"`
		Path      string `yaml:"path"`
		SourceRef struct {
			Kind string `yaml:"kind"`
			Name string `yaml:"name"`
		} `yaml:"sourceRef"`
		ServiceAccountName string `yaml:"serviceAccountName"`
		TargetNamespace    string `yaml:"targetNamespace"`
		DependsOn          []struct {
			Name string `yaml:"name"`
		} `yaml:"dependsOn"`
	} `yaml:"spec"`
}

// siteBlockProblems is everything wrong with one site's applications file.
func siteBlockProblems(rel, body string, apps []applications.Application, environments map[string][]string) []string {
	declared := map[string]applications.Application{}
	for _, a := range apps {
		declared[a.Name] = a
	}
	var docs []siteDocument
	dec := yaml.NewDecoder(strings.NewReader(body))
	for {
		var doc siteDocument
		err := dec.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			return []string{fmt.Sprintf("%s does not parse: %v", rel, err)}
		}
		if doc.Kind != "" {
			docs = append(docs, doc)
		}
	}

	var problems []string
	say := func(format string, args ...any) { problems = append(problems, rel+": "+fmt.Sprintf(format, args...)) }

	// Which Kustomization runs which application, by the directory it
	// reconciles.
	runs := map[string]string{} // application -> its Kustomization's name
	prefix := "./" + applications.Dir + "/"
	for _, d := range docs {
		if d.Kind != flux.Kustomization {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(d.Spec.Path, prefix), "/")
		if !strings.HasPrefix(d.Spec.Path, prefix) || len(parts) != 2 {
			say("the Kustomization %s reconciles %q. A block reconciles one environment's settings of one application: %s<application>/<environment>.", d.Metadata.Name, d.Spec.Path, prefix)
			continue
		}
		app, environment := parts[0], parts[1]
		a, ok := declared[app]
		if !ok {
			say("the Kustomization %s runs %s, and there is no application of that name under %s.", d.Metadata.Name, app, applications.Dir)
			continue
		}
		if _, twice := runs[app]; twice {
			say("two Kustomizations run %s. An application has one namespace, so a site runs it once.", app)
			continue
		}
		runs[app] = d.Metadata.Name
		if !slices.Contains(environments[app], environment) {
			say("the Kustomization %s runs %s with its settings for %q, and %s has settings for %v.", d.Metadata.Name, app, environment, a.Root, environments[app])
		}
		if want := applicationReconcilerPrefix + app; d.Spec.ServiceAccountName != want {
			say("the Kustomization %s reconciles %s as %q, not as %s. Reconciled as Flux itself it may create anything anywhere; as the application's own identity it may only manage the application's namespace.", d.Metadata.Name, app, d.Spec.ServiceAccountName, want)
		}
		if d.Spec.TargetNamespace != app {
			say("the Kustomization %s reconciles %s into the namespace %q. An application's objects land in the namespace named for it, whatever its manifests say.", d.Metadata.Name, app, d.Spec.TargetNamespace)
		}
		switch d.Spec.SourceRef.Kind {
		case flux.OCIRepository:
			if d.Spec.SourceRef.Name != app {
				say("the Kustomization %s runs %s from the release source %q. An application runs from its own release, the source named for it.", d.Metadata.Name, app, d.Spec.SourceRef.Name)
			}
		case "GitRepository":
			// Following main rather than a release: a site that is for
			// trying what the next release will be.
		default:
			say("the Kustomization %s runs %s from a %s. An application runs from its release, or follows the repository.", d.Metadata.Name, app, d.Spec.SourceRef.Kind)
		}
	}

	// Every source is some application's release, and that application is
	// run from it.
	sources := map[string]bool{}
	for _, d := range docs {
		switch d.Kind {
		case flux.Kustomization:
		case flux.OCIRepository:
			sources[d.Metadata.Name] = true
			if _, run := runs[d.Metadata.Name]; !run {
				say("the release source %s belongs to no application this file runs. A block is the release and the Kustomization that runs it; one without the other is a leftover.", d.Metadata.Name)
			}
			if !strings.HasSuffix(d.Spec.URL, "-"+d.Metadata.Name+"-release") {
				say("the release source %s reads %q, which is not that application's release.", d.Metadata.Name, d.Spec.URL)
			}
		default:
			say("a %s called %s. This file holds applications' blocks and nothing else: a release source and the Kustomization that runs it.", d.Kind, d.Metadata.Name)
		}
	}

	// What each application requires is here too, and starts first.
	for _, d := range docs {
		if d.Kind != flux.Kustomization {
			continue
		}
		for app, kustomization := range runs {
			if kustomization != d.Metadata.Name {
				continue
			}
			if d.Spec.SourceRef.Kind == flux.OCIRepository && d.Spec.SourceRef.Name == app && !sources[app] {
				say("the Kustomization %s runs %s from a release source this file does not have.", d.Metadata.Name, app)
			}
			var waitsFor []string
			for _, dep := range d.Spec.DependsOn {
				waitsFor = append(waitsFor, dep.Name)
			}
			for _, required := range declared[app].Requires {
				other, given := runs[required]
				switch {
				case !given:
					say("this site runs %s, which requires %s, and does not run %s. An application is given to a site with everything it requires.", app, required, required)
				case !slices.Contains(waitsFor, other):
					say("the Kustomization %s runs %s, which requires %s, and does not depend on %s. What an application requires is healthy before it starts.", d.Metadata.Name, app, required, other)
				}
			}
		}
	}

	// And the reading that takes no YAML library sees the same applications.
	assigned, err := applications.ParseAssigned(rel, body)
	var read []string
	for _, a := range assigned {
		read = append(read, a.Application)
	}
	var here []string
	for app := range runs {
		here = append(here, app)
	}
	sort.Strings(here)
	switch {
	case err != nil:
		say("the contractor cannot read it: %v", err)
	case len(problems) == 0 && strings.Join(read, ",") != strings.Join(here, ","):
		say("the contractor reads this site as running %v, and it runs %v. The contractor reads the file line by line, so a path written in a shape it does not expect is an application it gives no namespace or secrets to.", read, here)
	}
	sort.Strings(problems)
	return problems
}

// The check is held to what it claims, against files written here: a block
// that follows its application's declaration is accepted, and each way a
// block can depart from it is named.
func TestSiteBlockProblemsNamesEachWayABlockDepartsFromItsDeclaration(t *testing.T) {
	app := func(name string, requires ...string) applications.Application {
		return applications.Application{Name: name, Root: applications.Dir + "/" + name, Requires: requires}
	}
	apps := []applications.Application{app("thing"), app("needy", "thing")}
	environments := map[string][]string{"thing": {"production", "staging"}, "needy": {"production"}}
	source := func(name string) string {
		return "---\napiVersion: source.toolkit.fluxcd.io/v1\nkind: OCIRepository\nmetadata:\n  name: " + name + "\n  namespace: flux-system\nspec:\n  url: oci://${RELEASE_REPOSITORY}-" + name + "-release\n"
	}
	block := func(kustomization, application, environment, extra string) string {
		return "---\napiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nmetadata:\n  name: " + kustomization + "\n  namespace: flux-system\nspec:\n" +
			"  sourceRef:\n    kind: OCIRepository\n    name: " + application + "\n" +
			"  path: ./" + applications.Dir + "/" + application + "/" + environment + "\n" +
			"  serviceAccountName: application-" + application + "\n  targetNamespace: " + application + "\n" + extra
	}
	good := source("thing") + block("thing", "thing", "production", "  dependsOn:\n    - name: infra-configs\n")
	needy := source("needy") + block("needy", "needy", "production", "  dependsOn:\n    - name: infra-configs\n    - name: thing\n")

	for name, c := range map[string]struct {
		body string
		want []string
	}{
		"one application as declared":           {good, nil},
		"an application and what it requires":   {good + needy, nil},
		"a Kustomization under another name":    {source("thing") + block("workloads-production", "thing", "staging", ""), nil},
		"nothing in it yet":                     {"# nothing yet\n", nil},
		"following the repository":              {strings.Replace(block("thing", "thing", "production", ""), "kind: OCIRepository\n    name: thing", "kind: GitRepository\n    name: flux-system", 1), nil},
		"not YAML":                              {"kind: [\n", []string{"does not parse"}},
		"a path that is not an application's":   {source("thing") + strings.Replace(block("thing", "thing", "production", ""), "./"+applications.Dir+"/thing/production", "./clusters/core", 1), []string{"reconciles \"./clusters/core\"", "belongs to no application this file runs"}},
		"a path below an environment":           {source("thing") + strings.Replace(block("thing", "thing", "production", ""), "/thing/production", "/thing/production/extra", 1), []string{"reconciles", "belongs to no application this file runs"}},
		"an application nothing declares":       {source("ghost") + block("ghost", "ghost", "production", ""), []string{"no application of that name", "belongs to no application this file runs"}},
		"an environment it has no settings for": {source("thing") + block("thing", "thing", "development", ""), []string{`settings for "development"`}},
		"reconciled as Flux itself":             {source("thing") + strings.Replace(block("thing", "thing", "production", ""), "  serviceAccountName: application-thing\n", "", 1), []string{"not as application-thing"}},
		"reconciled as another's identity":      {source("thing") + strings.Replace(block("thing", "thing", "production", ""), "application-thing", "application-needy", 1), []string{"not as application-thing"}},
		"into another namespace":                {source("thing") + strings.Replace(block("thing", "thing", "production", ""), "targetNamespace: thing", "targetNamespace: flux-system", 1), []string{`into the namespace "flux-system"`}},
		"into no namespace of its own":          {source("thing") + strings.Replace(block("thing", "thing", "production", ""), "  targetNamespace: thing\n", "", 1), []string{`into the namespace ""`}},
		"from another application's release":    {source("thing") + source("needy") + strings.Replace(block("thing", "thing", "production", ""), "kind: OCIRepository\n    name: thing", "kind: OCIRepository\n    name: needy", 1), []string{`from the release source "needy"`, "belongs to no application this file runs"}},
		"from a release that is not here":       {block("thing", "thing", "production", ""), []string{"from a release source this file does not have"}},
		"from a bucket":                         {strings.Replace(block("thing", "thing", "production", ""), "kind: OCIRepository", "kind: Bucket", 1), []string{"from a Bucket"}},
		"a release of something else":           {strings.Replace(source("thing"), "-thing-release", "-other-release", 1) + block("thing", "thing", "production", ""), []string{"is not that application's release"}},
		"run twice":                             {good + block("again", "thing", "staging", ""), []string{"two Kustomizations run thing", "the contractor cannot read it"}},
		"without what it requires":              {source("needy") + block("needy", "needy", "production", ""), []string{"does not run thing"}},
		"not waiting for what it requires":      {good + source("needy") + block("needy", "needy", "production", "  dependsOn:\n    - name: infra-configs\n"), []string{"does not depend on thing"}},
		"something that is not a block":         {good + "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: stray\n", []string{"a ConfigMap called stray"}},
		"a path the contractor reads otherwise": {source("thing") + strings.Replace(block("thing", "thing", "production", ""), "  path: ./"+applications.Dir+"/thing/production\n", "  path: \"./"+applications.Dir+"/thing/production\"\n", 1), []string{"the contractor reads this site as running []"}},
		"a path in a shape only YAML reads":     {source("thing") + strings.Replace(block("thing", "thing", "production", ""), "  path: ./"+applications.Dir+"/thing/production\n", "  path:\n    ./"+applications.Dir+"/thing/production\n", 1), []string{"the contractor reads this site as running []"}},
	} {
		got := siteBlockProblems(applications.SiteFilePath("siteN"), c.body, apps, environments)
		if len(got) != len(c.want) {
			t.Errorf("%s: want %d problem(s), got %d:\n  %s", name, len(c.want), len(got), strings.Join(got, "\n  "))
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(strings.Join(got, "\n"), w) {
				t.Errorf("%s: no problem says %q:\n  %s", name, w, strings.Join(got, "\n  "))
			}
		}
	}
}
