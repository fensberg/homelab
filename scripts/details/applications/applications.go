// Package applications reads what each application declares about itself.
//
// An application is one directory, and says what it needs in one file.
//
// The estate's test of whether an application is modular is what removing it
// costs: its directory, and its block in each site that runs it (#590). That
// only holds if nothing outside the directory knows the application by name.
// So what the estate's general mechanisms need to know about an application -
// its secrets, the addresses it must be reached on, how it is built and
// released, where its upstream publishes, how to back it up before a
// teardown, which other applications it cannot run without - the application
// declares, in application.json in its own directory, and each mechanism
// reads every application's declaration instead of naming one.
//
// Here rather than in any one program, because the contractor, procurement,
// the superintendent and the repository's tests all read it, and OpenTofu
// reads the same file (modules/infrastructure/applications).
//
// Read from the repository rather than from the running pod, because what
// runs in production is a release, and a declaration that had to be released
// before it could be read would not protect the teardown that is about to
// happen.
package applications

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"homelab/details/holds"
	"homelab/details/sizing"
)

// Dir is where applications are, from the top of the repository: one
// directory each, holding everything that is that application's.
const Dir = "modules/applications"

// Declaration is the file an application declares itself in.
const Declaration = "application.json"

// Kustomization is the file that makes a directory one kustomize builds.
const Kustomization = "kustomization.yaml"

// Base is the directory of an application's manifests that every environment
// builds on. Every other directory of an application holding a kustomization
// is one environment's settings.
const Base = "base"

// Pins is the file in an application's directory that pins what its image is
// built from, beside the estate's own pins (scripts/versions.env).
const Pins = "pins.env"

// Application is one application's declaration.
type Application struct {
	// Comment is for the reader of the file, which has no other way to
	// carry one. Nothing reads it.
	Comment []string `json:"_comment,omitempty"`
	// Requires names the other applications this one cannot run without.
	// It is the one place an application may name another: a site given
	// this one must run those too, and they start first.
	Requires []string `json:"requires"`
	// Routes are the Services that need an address known before they exist,
	// because an enrolled device is routed to it: the Service's name, and a
	// host number in every site's service range.
	Routes map[string]int `json:"routes,omitempty"`
	// Secrets are the Secrets the application's namespace is given before
	// anything of the application runs: the Secret's name, then each key and
	// where its value comes from.
	Secrets map[string]map[string]Source `json:"secrets,omitempty"`
	// Release is how a release of the application is made from the image
	// built for it. An application that is not released leaves it out.
	Release *Release `json:"release,omitempty"`
	// Upstream is where the software the image carries is published, for the
	// expediter to keep the application on what its supplier ships.
	Upstream *Upstream `json:"upstream,omitempty"`
	// BeforeTeardown is how to take a backup now, before the site the
	// application runs on is destroyed (#588). An application with no data
	// a teardown would lose leaves it out.
	BeforeTeardown *BeforeTeardown `json:"before_teardown,omitempty"`
	// Holds is what the application holds that is worth keeping, and for
	// how long (homelab/details/holds). The safety officer reads it before
	// anything the application runs on is destroyed.
	Holds []holds.Asset `json:"holds,omitempty"`
	// SizedFrom is where each of the application's reservations came from
	// (homelab/details/sizing): by the workload that reserves, then by the
	// container in it. The reservation itself is the manifest's to say.
	SizedFrom sizing.Declared `json:"sized_from,omitempty"`

	// Name is the application's directory, which is also its namespace and
	// its item in a site's vault. Root is that directory and Path its
	// declaration, from the top of the repository.
	Name string `json:"-"`
	Root string `json:"-"`
	Path string `json:"-"`
}

// Source is where one key of a Secret gets its value. Exactly one is set.
type Source struct {
	// Vault is a field of the application's item in the site's vault.
	Vault string `json:"vault,omitempty"`
	// Generated is such a field that the estate creates when it is not
	// there, because nobody ever needs to type or read it.
	Generated string `json:"generated,omitempty"`
	// Storage is one fact about the bucket the estate keeps for the
	// environment the site runs the application in.
	Storage string `json:"storage,omitempty"`
	// Value is the value itself, for what is not a secret but is read from
	// the same Secret as the rest.
	Value *string `json:"value,omitempty"`
}

// What a Secret may be told about the application's bucket, as the config
// names each: the bucket, the credential scoped to it, and where it is
// served from.
const (
	StorageBucket          = "bucket"
	StorageAccessKeyID     = "access_key_id"
	StorageSecretAccessKey = "secret_access_key"
	StorageEndpoint        = "endpoint"
)

// StorageFacts are those, in order.
var StorageFacts = []string{StorageAccessKeyID, StorageBucket, StorageEndpoint, StorageSecretAccessKey}

// Release is how a release's version is read from the image built for it.
type Release struct {
	Version struct {
		// The environment variables the image needs set in order to start,
		// each given a random throwaway value.
		Env []string `json:"env"`
		// The sed pattern that captures the version from what it prints.
		Pattern string `json:"pattern"`
		// A line the software really prints, and the version the pattern
		// must read from it. The fabricator's own step is run against it, so
		// a pattern that reads nothing is refused on the pull request that
		// wrote it and not by the first build that needs it.
		Example struct {
			Line    string `json:"line"`
			Version string `json:"version"`
		} `json:"example"`
	} `json:"version"`
}

// Steam is the one kind of upstream the expediter knows how to ask.
const Steam = "steam"

// Upstream is where an application's software is published.
type Upstream struct {
	// Kind is which supplier's protocol to speak.
	Kind string `json:"kind"`
	// App is what to ask the supplier about: for Steam, the appid whose
	// public build is the one to run.
	App string `json:"app"`
	// News is where the supplier announces a new build, which is only a
	// reason to ask properly: for Steam, the appid whose announcements
	// carry the supplier's own posts.
	News string `json:"news"`
	// Pin is the line in the application's pins file that records the
	// build its image is made from.
	Pin string `json:"pin"`
}

// BeforeTeardown is how one application takes a backup now.
type BeforeTeardown struct {
	// What is being saved, in the words a progress line should use.
	What string `json:"what"`
	// How to pick the application's pods in its namespace.
	Selector string `json:"selector"`
	// The container to run the command in, and the command. It takes a
	// backup now and exits zero only when it has.
	Container string   `json:"container"`
	Command   []string `json:"command"`

	// Workload is the application that declared it, Namespace where its
	// pods are, and Path the declaration.
	Workload  string `json:"-"`
	Namespace string `json:"-"`
	Path      string `json:"-"`
}

var (
	// A name Kubernetes takes for a namespace, a Service or a Secret.
	objectName = regexp.MustCompile(`^[a-z]([a-z0-9-]*[a-z0-9])?$`)
	vaultField = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)
	pinName    = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	number     = regexp.MustCompile(`^[0-9]+$`)
)

// Read reads every application's declaration, in name order.
//
// Every directory under Dir is an application, and one without a declaration
// is an error rather than an application with nothing to say: a mechanism
// that reads these would otherwise pass over it in silence. So is a
// declaration that does not parse, names a field nothing reads, requires an
// application that is not there, or claims a route another already has.
func Read(repoRoot string) ([]Application, error) {
	dir := filepath.Join(repoRoot, filepath.FromSlash(Dir))
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
		root := Dir + "/" + e.Name()
		rel := root + "/" + Declaration
		raw, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			return nil, fmt.Errorf("%s is an application with no %s, so nothing that reads the declarations knows it is there: %w", root, Declaration, err)
		}
		a, err := Parse(e.Name(), raw)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if err := together(out); err != nil {
		return nil, err
	}
	return out, nil
}

// Parse reads one application's declaration, and refuses one that leaves out
// what it must say. What can only be judged with every application in view -
// requirements and shared routes - is Read's.
func Parse(name string, raw []byte) (Application, error) {
	a := Application{Name: name, Root: Dir + "/" + name}
	a.Path = a.Root + "/" + Declaration
	if !objectName.MatchString(name) {
		return a, fmt.Errorf("%s is not a name a namespace can have, and an application's directory names its namespace", a.Root)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return a, fmt.Errorf("%s is not a declaration this reads: %w", a.Path, err)
	}
	for route, host := range a.Routes {
		if !objectName.MatchString(route) {
			return a, fmt.Errorf("%s routes to %q, which is not a name a Service can have", a.Path, route)
		}
		// The band of a site's service range Kubernetes never allocates
		// from by itself; the address plan refuses the same.
		if host < 1 || host > 63 {
			return a, fmt.Errorf("%s gives the route %s the host number %d; a fixed address is from 1 to 63", a.Path, route, host)
		}
	}
	for secret, keys := range a.Secrets {
		if !objectName.MatchString(secret) {
			return a, fmt.Errorf("%s declares the secret %q, which is not a name a Secret can have", a.Path, secret)
		}
		if len(keys) == 0 {
			return a, fmt.Errorf("%s declares the secret %s with nothing in it", a.Path, secret)
		}
		for key, s := range keys {
			if err := s.check(); err != nil {
				return a, fmt.Errorf("%s: %s.%s %w", a.Path, secret, key, err)
			}
		}
	}
	if r := a.Release; r != nil {
		switch {
		case strings.TrimSpace(r.Version.Pattern) == "":
			return a, fmt.Errorf("%s makes a release and gives no release.version.pattern, so there is no telling what version a build is", a.Path)
		case strings.TrimSpace(r.Version.Example.Line) == "" || strings.TrimSpace(r.Version.Example.Version) == "":
			return a, fmt.Errorf("%s gives no release.version.example - a line the software prints and the version to read from it - so nothing shows its pattern reads a version at all", a.Path)
		}
	}
	if u := a.Upstream; u != nil {
		switch {
		case u.Kind != Steam:
			return a, fmt.Errorf("%s names an upstream of kind %q, and %s is the only kind the expediter can ask", a.Path, u.Kind, Steam)
		case !number.MatchString(u.App) || !number.MatchString(u.News):
			return a, fmt.Errorf("%s names an upstream whose app and news are not both Steam appids", a.Path)
		case !pinName.MatchString(u.Pin):
			return a, fmt.Errorf("%s names the upstream pin %q, which is not the name of a line in a pins file", a.Path, u.Pin)
		}
	}
	if b := a.BeforeTeardown; b != nil {
		b.Workload, b.Namespace, b.Path = a.Name, a.Name, a.Path
		for field, value := range map[string]string{"what": b.What, "selector": b.Selector, "container": b.Container} {
			if strings.TrimSpace(value) == "" {
				return a, fmt.Errorf("%s leaves before_teardown.%s empty, so there is no telling what to back up or where", a.Path, field)
			}
		}
		if len(b.Command) == 0 {
			return a, fmt.Errorf("%s names no before_teardown.command, so there is nothing to run that would take the backup", a.Path)
		}
	}
	if err := a.SizedFrom.Check(a.Path); err != nil {
		return a, err
	}
	held, err := holds.Owned(a.Holds, a.Name, a.Path)
	if err != nil {
		return a, err
	}
	a.Holds = held
	return a, nil
}

func (s Source) check() error {
	set := 0
	for _, v := range []string{s.Vault, s.Generated, s.Storage} {
		if v != "" {
			set++
		}
	}
	if s.Value != nil {
		set++
	}
	switch {
	case set != 1:
		return fmt.Errorf("must say exactly one of vault, generated, storage and value")
	case s.Vault != "" && !vaultField.MatchString(s.Vault), s.Generated != "" && !vaultField.MatchString(s.Generated):
		return fmt.Errorf("names a vault field that is not lower-case words joined by underscores")
	case s.Storage != "" && !slices.Contains(StorageFacts, s.Storage):
		return fmt.Errorf("asks for the bucket's %q, and what a bucket has is %s", s.Storage, strings.Join(StorageFacts, ", "))
	}
	return nil
}

// together is what can only be judged of every application at once.
func together(apps []Application) error {
	names := make([]string, len(apps))
	for i, a := range apps {
		names[i] = a.Name
	}
	routes, hosts := map[string]string{}, map[int]string{}
	for _, a := range apps {
		for _, r := range a.Requires {
			switch {
			case r == a.Name:
				return fmt.Errorf("%s requires itself", a.Path)
			case !slices.Contains(names, r):
				return fmt.Errorf("%s requires %q, and there is no application of that name (%s)", a.Path, r, strings.Join(names, ", "))
			}
		}
		for _, route := range slices.Sorted(maps.Keys(a.Routes)) {
			if other, taken := routes[route]; taken {
				return fmt.Errorf("%s and %s both declare the route %s, so two Services would be given one address", other, a.Path, route)
			}
			if other, taken := hosts[a.Routes[route]]; taken {
				return fmt.Errorf("%s and %s both declare a route at host number %d, so two Services would claim one address", other, a.Path, a.Routes[route])
			}
			routes[route], hosts[a.Routes[route]] = a.Path, a.Path
		}
	}
	if cycle := requiresCycle(apps); cycle != "" {
		return fmt.Errorf("applications require each other in a circle (%s), so none of them could start first", cycle)
	}
	return nil
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

// VaultFields is every field of the application's vault item its secrets
// read, in order, and Generated those of them the estate creates.
func (a Application) VaultFields() []string {
	return a.fields(func(s Source) string { return s.Vault + s.Generated })
}

// GeneratedFields is the vault fields the estate creates when they are not
// there.
func (a Application) GeneratedFields() []string {
	return a.fields(func(s Source) string { return s.Generated })
}

func (a Application) fields(of func(Source) string) []string {
	seen := map[string]bool{}
	for _, keys := range a.Secrets {
		for _, s := range keys {
			if f := of(s); f != "" {
				seen[f] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// Image is the application's build context, holding its Dockerfile.
func (a Application) Image() string { return a.Root + "/image" }

// PinsFile is where the application pins what its image is built from.
func (a Application) PinsFile() string { return a.Root + "/" + Pins }

// Builds reports whether the application has an image the estate builds.
func (a Application) Builds(repoRoot string) bool {
	_, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(a.Image()), "Dockerfile"))
	return err == nil
}

// Environments is the environments the application has settings for: every
// directory of its own, other than the base, that holds a kustomization.
func (a Application) Environments(repoRoot string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(repoRoot, filepath.FromSlash(a.Root)))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == Base {
			continue
		}
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(a.Root), e.Name(), Kustomization)); err == nil {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// Backups is every application's before_teardown, for the teardown that asks
// each to take a backup.
func Backups(repoRoot string) ([]BeforeTeardown, error) {
	apps, err := Read(repoRoot)
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

// ManifestsEnv names the directory an application's own tests are handed its
// manifests in: every YAML file of the application as a JSON array of its
// documents, at the same path with .json for its extension. The repository's
// guards write it and run the tests (tests/go/repo); the tests read it with
// ReadManifest, which is what lets them hold their manifests to anything
// with the standard library alone.
const ManifestsEnv = "APPLICATION_MANIFESTS"

// ReadManifest reads one of an application's manifests, by its path inside
// the application's directory, into out: one element per document.
func ReadManifest(rel string, out any) error {
	dir := os.Getenv(ManifestsEnv)
	if dir == "" {
		return fmt.Errorf("%s is not set. An application's own tests are run by the repository's guards, which hand them the application's manifests: `task test`, or `go test -C tests/go ./repo -run TestEveryApplicationsOwnTestsPass`", ManifestsEnv)
	}
	body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(strings.TrimSuffix(rel, filepath.Ext(rel))+".json")))
	if err != nil {
		return fmt.Errorf("reading the manifest %s: %w", rel, err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("reading the manifest %s: %w", rel, err)
	}
	return nil
}

// SitesDir is the Flux tree, where each site that was given work has a
// directory, and SiteFile the file in it that holds one block per
// application the site runs.
const (
	SitesDir = "clusters"
	SiteFile = "applications.yaml"
)

// Assignment is one application a site runs, and which environment's
// settings it runs it with.
type Assignment struct{ Application, Environment string }

// assignedPath is the line of a block that says which application it is and
// which environment's settings it reconciles: the Kustomization's path, into
// the application's own directory.
var assignedPath = regexp.MustCompile(`^\s+path:\s*\./` + regexp.QuoteMeta(Dir) + `/([^/\s]+)/([^/\s]+)\s*$`)

// SiteFilePath is where a site's applications are assigned, from the top of
// the repository.
func SiteFilePath(site string) string { return SitesDir + "/" + site + "/" + SiteFile }

// Assigned is the applications a site was given, in name order. A site with
// no such file was given none.
//
// Read line by line rather than through a YAML library, because the programs
// that read it take no dependencies; the file's shape is this repository's
// own, and the tests hold every block in it to that shape.
func Assigned(repoRoot, site string) ([]Assignment, error) {
	body, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(SiteFilePath(site))))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseAssigned(SiteFilePath(site), string(body))
}

// ParseAssigned reads the assignments out of a site file's text.
func ParseAssigned(rel, body string) ([]Assignment, error) {
	var out []Assignment
	seen := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		m := assignedPath.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if seen[m[1]] {
			return nil, fmt.Errorf("%s gives the site %s twice, and an application has one namespace", rel, m[1])
		}
		seen[m[1]] = true
		out = append(out, Assignment{Application: m[1], Environment: m[2]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Application < out[j].Application })
	return out, nil
}

// FluxObject is one object a site file declares for Flux to reconcile: an
// application's release, or the Kustomization that runs it.
type FluxObject struct{ Kind, Namespace, Name string }

// Work is a site's work as its cluster holds it: the objects its site file
// declares, and the namespaces its applications run in. It is what tells the
// work a site was given apart from the core the site is made of, by what the
// site file says and never by what anything is called.
type Work struct {
	Objects    []FluxObject
	Namespaces []string
}

// Holds reports whether an object in the cluster is the site's work: one the
// site file declares, or anything in an application's own namespace, which
// is where everything a release brings lands. Whatever this does not hold is
// the core.
func (w Work) Holds(kind, namespace, name string) bool {
	for _, ns := range w.Namespaces {
		if namespace == ns {
			return true
		}
	}
	for _, o := range w.Objects {
		if o.Kind == kind && o.Namespace == namespace && o.Name == name {
			return true
		}
	}
	return false
}

var (
	declaredKind = regexp.MustCompile(`^kind:\s*(\S+)\s*$`)
	declaredMeta = regexp.MustCompile(`^  (name|namespace):\s*(\S+)\s*$`)
)

// ParseDeclared reads the objects a site file declares: each document's kind
// and, from its metadata, its name and namespace. A document with a kind and
// no name is an error, because an object this cannot name is one it would
// leave to be read as the core.
func ParseDeclared(rel, body string) ([]FluxObject, error) {
	var out []FluxObject
	for _, doc := range strings.Split("\n"+body, "\n---") {
		var o FluxObject
		inMeta := false
		for _, line := range strings.Split(doc, "\n") {
			if m := declaredKind.FindStringSubmatch(line); m != nil {
				o.Kind = m[1]
			}
			switch {
			case line == "metadata:":
				inMeta = true
			case line != "" && !strings.HasPrefix(line, " "):
				inMeta = false
			case inMeta:
				if m := declaredMeta.FindStringSubmatch(line); m != nil {
					if m[1] == "name" {
						o.Name = m[2]
					} else {
						o.Namespace = m[2]
					}
				}
			}
		}
		if o.Kind == "" {
			continue
		}
		if o.Name == "" {
			return nil, fmt.Errorf("%s declares a %s with no name, so it cannot be told apart from the core", rel, o.Kind)
		}
		out = append(out, o)
	}
	return out, nil
}

// SiteWork is the work a site was given. A site with no site file was given
// none, and everything it runs is the core.
func SiteWork(repoRoot, site string) (Work, error) {
	rel := SiteFilePath(site)
	body, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	if os.IsNotExist(err) {
		return Work{}, nil
	}
	if err != nil {
		return Work{}, err
	}
	objects, err := ParseDeclared(rel, string(body))
	if err != nil {
		return Work{}, err
	}
	assigned, err := ParseAssigned(rel, string(body))
	if err != nil {
		return Work{}, err
	}
	w := Work{Objects: objects}
	for _, a := range assigned {
		w.Namespaces = append(w.Namespaces, a.Application)
	}
	return w, nil
}
