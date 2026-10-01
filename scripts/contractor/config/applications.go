package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// An application is one directory, and says what it needs in one file.
//
// The estate's test of whether an application is modular is what removing it
// costs: its directory, and its block in each site that runs it (#590). That
// only holds if nothing outside the directory knows the application by name.
// So what the estate's general mechanisms need to know about an application -
// how to back it up before a teardown, which other applications it cannot run
// without - the application declares, in application.json in its own
// directory, and each mechanism reads every application's declaration instead
// of naming one.
//
// Read from the repository rather than from the running pod, because what
// runs in production is a release, and a declaration that had to be released
// before it could be read would not protect the teardown that is about to
// happen.

// ApplicationsDir is where applications are, from the top of the repository:
// one directory each, holding everything that is that application's.
const ApplicationsDir = "modules/applications"

// ApplicationDeclaration is the file an application declares itself in.
const ApplicationDeclaration = "application.json"

// Application is one application's declaration.
type Application struct {
	// Requires names the other applications this one cannot run without.
	// It is the one place an application may name another: a site given
	// this one must run those too, and they start first.
	Requires []string `json:"requires"`
	// BeforeTeardown is how to take a backup now, before the site the
	// application runs on is destroyed (#588). An application with no data
	// a teardown would lose leaves it out.
	BeforeTeardown *BeforeTeardown `json:"before_teardown,omitempty"`

	// Name is the application's directory, and Path its declaration, from
	// the top of the repository.
	Name string `json:"-"`
	Path string `json:"-"`
}

// BeforeTeardown is how one application takes a backup now.
type BeforeTeardown struct {
	// What is being saved, in the words a progress line should use.
	What string `json:"what"`
	// Where the application's pods are, and how to pick them.
	Namespace string `json:"namespace"`
	Selector  string `json:"selector"`
	// The container to run the command in, and the command. It takes a
	// backup now and exits zero only when it has.
	Container string   `json:"container"`
	Command   []string `json:"command"`

	// Workload is the application that declared it, and Path where.
	Workload string `json:"-"`
	Path     string `json:"-"`
}

// Applications reads every application's declaration, in name order.
//
// Every directory under ApplicationsDir is an application, and one without a
// declaration is an error rather than an application with nothing to say: a
// mechanism that reads these would otherwise pass over it in silence. So is a
// declaration that does not parse, names a field nothing reads, or requires
// an application that is not there.
func Applications(repoRoot string) ([]Application, error) {
	dir := filepath.Join(repoRoot, filepath.FromSlash(ApplicationsDir))
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Application
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rel := ApplicationsDir + "/" + e.Name() + "/" + ApplicationDeclaration
		raw, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			return nil, fmt.Errorf("%s/%s is an application with no %s, so nothing that reads the declarations knows it is there: %w", ApplicationsDir, e.Name(), ApplicationDeclaration, err)
		}
		a := Application{Name: e.Name(), Path: rel}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&a); err != nil {
			return nil, fmt.Errorf("%s is not a declaration this reads: %w", rel, err)
		}
		if b := a.BeforeTeardown; b != nil {
			b.Workload, b.Path = a.Name, rel
			for field, value := range map[string]string{"what": b.What, "namespace": b.Namespace, "selector": b.Selector, "container": b.Container} {
				if strings.TrimSpace(value) == "" {
					return nil, fmt.Errorf("%s leaves before_teardown.%s empty, so there is no telling what to back up or where", rel, field)
				}
			}
			if len(b.Command) == 0 {
				return nil, fmt.Errorf("%s names no before_teardown.command, so there is nothing to run that would take the backup", rel)
			}
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	names := make([]string, len(out))
	for i, a := range out {
		names[i] = a.Name
	}
	for _, a := range out {
		for _, r := range a.Requires {
			switch {
			case r == a.Name:
				return nil, fmt.Errorf("%s requires itself", a.Path)
			case !slices.Contains(names, r):
				return nil, fmt.Errorf("%s requires %q, and there is no application of that name (%s)", a.Path, r, strings.Join(names, ", "))
			}
		}
	}
	if cycle := requiresCycle(out); cycle != "" {
		return nil, fmt.Errorf("applications require each other in a circle (%s), so none of them could start first", cycle)
	}
	return out, nil
}

// requiresCycle is a circle of requirements, as "a -> b -> a", or "" when
// there is none.
func requiresCycle(apps []Application) string {
	requires := map[string][]string{}
	for _, a := range apps {
		requires[a.Name] = a.Requires
	}
	const visiting, done = 1, 2
	state := map[string]int{}
	var path []string
	var walk func(name string) string
	walk = func(name string) string {
		switch state[name] {
		case done:
			return ""
		case visiting:
			at := slices.Index(path, name)
			return strings.Join(append(append([]string{}, path[at:]...), name), " -> ")
		}
		state[name] = visiting
		path = append(path, name)
		for _, r := range requires[name] {
			if c := walk(r); c != "" {
				return c
			}
		}
		path = path[:len(path)-1]
		state[name] = done
		return ""
	}
	for _, a := range apps {
		if c := walk(a.Name); c != "" {
			return c
		}
	}
	return ""
}

// DeclaredBackups is every application's before_teardown, for the teardown
// that asks each to take a backup.
func DeclaredBackups(repoRoot string) ([]BeforeTeardown, error) {
	apps, err := Applications(repoRoot)
	if err != nil {
		return nil, err
	}
	var out []BeforeTeardown
	for _, a := range apps {
		if a.BeforeTeardown != nil {
			out = append(out, *a.BeforeTeardown)
		}
	}
	return out, nil
}
