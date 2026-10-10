// The drafter redraws what the estate declares in the form each program
// reads.
//
// Nearly everything here is declared in a format some other tool insists on:
// YAML for Kubernetes, Flux, the workflows and the lists this repository
// keeps; a directory kustomize assembles; a file of names and values. The
// programs that decide things have to read those declarations, and the ones
// that hold power take no dependencies, so each read them by matching lines
// - which is reading a format by what it usually looks like.
//
// So one program reads every format properly and hands back JSON, which the
// standard library reads. It is the central translator and nothing else: it
// holds no key, has no opinion about what it draws, and never writes to the
// estate. A format it has not been taught is refused by name, never guessed
// at.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const usage = `drafter redraws what the estate declares as JSON.

usage: drafter draw [-root DIR] [-build DIR]... [PATH]...

  PATH        A file, or a directory read all the way down. Each file is
              redrawn by what it is: YAML as a list of its documents, JSON
              as itself, a file of NAME=value lines as an object.
  -build DIR  A directory kustomize assembles, built the way the cluster
              builds it and redrawn as a list of what comes out.
  -root DIR   Where the paths are from. They are named in the answer as
              they were given.

The answer is one JSON object on standard output: each file by its path,
and each built directory by "built:" and its path.

usage: drafter duplicates [-root DIR] PATH...

  Names every key a YAML file writes twice in one mapping, by file. A file
  that writes none is answered with an empty list. Drawing such a file is
  refused, since the second key would silently replace the first; this is
  for whoever needs to know which keys they are.
`

// built is what a built directory is named by in the answer, before its
// path.
const built = "built:"

type dirs []string

func (d *dirs) String() string     { return strings.Join(*d, ",") }
func (d *dirs) Set(v string) error { *d = append(*d, v); return nil }

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "draw":
		os.Exit(draw(os.Args[2:], os.Stdout, os.Stderr, kustomized))
	case "duplicates":
		os.Exit(duplicates(os.Args[2:], os.Stdout, os.Stderr))
	}
	fmt.Fprint(os.Stderr, usage)
	os.Exit(2)
}

// duplicates is the verb that names the keys a file writes twice.
func duplicates(args []string, out, complaints io.Writer) int {
	flags := flag.NewFlagSet("duplicates", flag.ContinueOnError)
	flags.SetOutput(complaints)
	root := flags.String("root", ".", "Where the paths are from.")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() == 0 {
		fmt.Fprintln(complaints, "error: duplicates needs a file to read")
		return 2
	}
	found := map[string][]string{}
	for _, given := range flags.Args() {
		body, err := os.ReadFile(filepath.Join(*root, filepath.FromSlash(given)))
		if err != nil {
			fmt.Fprintln(complaints, "error:", err)
			return 1
		}
		names, err := writtenTwice(body)
		if err != nil {
			fmt.Fprintf(complaints, "error: %s: %v\n", given, err)
			return 1
		}
		found[given] = names
	}
	if err := json.NewEncoder(out).Encode(found); err != nil {
		fmt.Fprintln(complaints, "error:", err)
		return 1
	}
	return 0
}

// writtenTwice is every key some mapping of a YAML text holds more than
// once, each named once, in the order met. The text is read as its own
// structure and not into values, because reading it into values is what
// refuses a key written twice.
func writtenTwice(body []byte) ([]string, error) {
	names := []string{}
	seen := map[string]bool{}
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.MappingNode {
			here := map[string]bool{}
			for i := 0; i+1 < len(n.Content); i += 2 {
				key := n.Content[i].Value
				if here[key] && !seen[key] {
					seen[key] = true
					names = append(names, key)
				}
				here[key] = true
			}
		}
		for _, child := range n.Content {
			walk(child)
		}
	}
	dec := yaml.NewDecoder(bytes.NewReader(body))
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return names, nil
		}
		if err != nil {
			return nil, fmt.Errorf("not YAML that can be read: %w", err)
		}
		walk(&doc)
	}
}

// draw is the verb, with the way a directory is built handed to it.
func draw(args []string, out, complaints io.Writer, build func(dir string) ([]byte, error)) int {
	flags := flag.NewFlagSet("draw", flag.ContinueOnError)
	flags.SetOutput(complaints)
	root := flags.String("root", ".", "Where the paths are from.")
	var builds dirs
	flags.Var(&builds, "build", "A directory kustomize assembles. May be given more than once.")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if len(builds) == 0 && flags.NArg() == 0 {
		fmt.Fprintln(complaints, "error: draw needs something to draw")
		return 2
	}

	drawn := map[string]any{}
	for _, given := range flags.Args() {
		if err := each(*root, given, drawn); err != nil {
			fmt.Fprintln(complaints, "error:", err)
			return 1
		}
	}
	for _, dir := range builds {
		body, err := build(filepath.Join(*root, filepath.FromSlash(dir)))
		if err != nil {
			fmt.Fprintf(complaints, "error: %s could not be built: %v\n", dir, err)
			return 1
		}
		docs, err := documents(body)
		if err != nil {
			fmt.Fprintf(complaints, "error: what %s builds to: %v\n", dir, err)
			return 1
		}
		drawn[built+dir] = docs
	}
	enc := json.NewEncoder(out)
	if err := enc.Encode(drawn); err != nil {
		fmt.Fprintln(complaints, "error:", err)
		return 1
	}
	return 0
}

// each redraws one path: a file, or every file under a directory that the
// drafter knows how to read. A file named outright that it cannot read is
// refused; one met while walking a directory is passed over, because a
// directory holds what it holds.
func each(root, given string, drawn map[string]any) error {
	start := filepath.Join(root, filepath.FromSlash(given))
	info, err := os.Stat(start)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		value, err := file(start)
		if err != nil {
			return fmt.Errorf("%s: %w", given, err)
		}
		drawn[given] = value
		return nil
	}
	return filepath.WalkDir(start, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if _, known := readers[filepath.Ext(path)]; !known {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		value, err := file(path)
		if err != nil {
			return fmt.Errorf("%s: %w", filepath.ToSlash(rel), err)
		}
		drawn[filepath.ToSlash(rel)] = value
		return nil
	})
}

// readers is every format the drafter has been taught, by the ending of the
// file's name. This table is how it learns another.
var readers = map[string]func([]byte) (any, error){
	".yaml": func(b []byte) (any, error) { return documents(b) },
	".yml":  func(b []byte) (any, error) { return documents(b) },
	".json": asItIs,
	".env":  namesAndValues,
}

func file(path string) (any, error) {
	read, known := readers[filepath.Ext(path)]
	if !known {
		return nil, fmt.Errorf("the drafter has not been taught to read a %q file; it reads %s", filepath.Ext(path), strings.Join(taught(), ", "))
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return read(body)
}

// documents is a YAML file as the list of its documents. A file may hold
// several, and one that is empty - a separator with nothing after it - is
// not a document.
func documents(body []byte) ([]any, error) {
	docs := []any{}
	dec := yaml.NewDecoder(bytes.NewReader(body))
	for {
		var doc any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return docs, nil
		}
		if err != nil {
			return nil, fmt.Errorf("not YAML that can be read: %w", err)
		}
		if doc == nil {
			continue
		}
		docs = append(docs, plain(doc))
	}
}

// plain makes what the YAML library returns into what JSON can say. YAML
// allows a mapping's keys to be anything; JSON's are text, so a key that
// is a number or a truth is written as it reads.
func plain(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, item := range x {
			x[k] = plain(item)
		}
		return x
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			out[fmt.Sprint(k)] = plain(item)
		}
		return out
	case []any:
		for i, item := range x {
			x[i] = plain(item)
		}
		return x
	}
	return v
}

// asItIs is a JSON file, checked and passed through.
func asItIs(body []byte) (any, error) {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("not JSON that can be read: %w", err)
	}
	return v, nil
}

// namesAndValues is a file of NAME=value lines as one object. Comments and
// blank lines are passed over; a line that is neither is refused, because
// a setting dropped in silence is a setting that is not applied.
func namesAndValues(body []byte) (any, error) {
	out := map[string]any{}
	scan := bufio.NewScanner(bytes.NewReader(body))
	for n := 1; scan.Scan(); n++ {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("line %d is not NAME=value", n)
		}
		out[strings.TrimSpace(name)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return out, scan.Err()
}

// kustomized builds a directory with the cluster's own tool.
func kustomized(dir string) ([]byte, error) {
	cmd := exec.Command("kubectl", "kustomize", dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("kubectl kustomize: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// taught is every ending the drafter reads, in order, for whoever asks.
func taught() []string {
	out := make([]string, 0, len(readers))
	for ext := range readers {
		out = append(out, ext)
	}
	sort.Strings(out)
	return out
}
