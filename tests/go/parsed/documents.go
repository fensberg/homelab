package parsed

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Setting is one line of a systemd unit: its section, its key and its
// value, in the order written.
type Setting struct {
	Section, Key, Value string
}

// UnitOf reads a systemd unit, or a drop-in for one, as the settings it
// makes. A line that is a comment, blank, or not a setting says nothing;
// a setting before any section is refused, because systemd refuses it.
func UnitOf(src string) ([]Setting, error) {
	var out []Setting
	section := ""
	for i, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";"):
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			section = strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
		default:
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				return nil, fmt.Errorf("line %d is not a setting: %q", i+1, line)
			}
			if section == "" {
				return nil, fmt.Errorf("line %d sets %s before any section", i+1, strings.TrimSpace(key))
			}
			out = append(out, Setting{section, strings.TrimSpace(key), strings.TrimSpace(value)})
		}
	}
	return out, nil
}

// UnitCommands is every command a unit's Exec settings run, with each
// sh -c script read as the commands in it.
func UnitCommands(name string, settings []Setting) ([]Command, error) {
	var out []Command
	for _, s := range settings {
		if !strings.HasPrefix(s.Key, "Exec") {
			continue
		}
		// A leading -, @, + or ! changes how systemd runs the line, not what.
		commands, err := TemplateCommands(name, strings.TrimLeft(s.Value, "-@+!:"))
		if err != nil {
			return nil, err
		}
		expanded, err := ExpandShells(name, commands)
		if err != nil {
			return nil, err
		}
		out = append(out, expanded...)
	}
	return out, nil
}

// CloudConfigOf reads a cloud-init document a task writes: a YAML mapping,
// once the template statements ({% ... %}) that wrap its lines are set
// aside. Its {{ expressions }} stay in its values as written.
func CloudConfigOf(src string) (map[string]any, error) {
	var kept []string
	for _, line := range strings.Split(src, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "{%") {
			kept = append(kept, line)
		}
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(strings.Join(kept, "\n")), &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// RunCommands is every command a cloud-init document's runcmd runs: a line
// as the shell reads it, a list as the words it is, and each sh -c script
// in either as the commands in it.
func RunCommands(name string, doc map[string]any) ([]Command, error) {
	items, _ := doc["runcmd"].([]any)
	var commands []Command
	for _, item := range items {
		switch v := item.(type) {
		case string:
			parsed, err := TemplateCommands(name, v)
			if err != nil {
				return nil, err
			}
			commands = append(commands, parsed...)
		case []any:
			var words []string
			for _, w := range v {
				words = append(words, fmt.Sprint(w))
			}
			commands = append(commands, Command{Words: words, Assigns: map[string]string{}})
		default:
			return nil, fmt.Errorf("a runcmd entry is a %T", item)
		}
	}
	return ExpandShells(name, commands)
}

// Scalar is one value a decoded document holds - never a key - and where:
// the keys and indexes that lead to it.
type Scalar struct {
	Path  []string
	Value string
}

// At is the scalar's path as one string: spec.containers[0].image.
func (s Scalar) At() string { return strings.Join(s.Path, ".") }

// Key is the last key that leads to the scalar, past any index.
func (s Scalar) Key() string {
	for i := len(s.Path) - 1; i >= 0; i-- {
		if !strings.HasPrefix(s.Path[i], "[") {
			return s.Path[i]
		}
	}
	return ""
}

// Scalars is every value a decoded document holds, each with its path, in
// key order. A number or a boolean is given as it prints, and a key that
// holds nothing as an empty value, so that every key is on some path.
func Scalars(doc any) []Scalar {
	var out []Scalar
	var walk func(path []string, v any)
	walk = func(path []string, v any) {
		switch v := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(append(append([]string{}, path...), k), v[k])
			}
		case []any:
			for i, x := range v {
				walk(append(append([]string{}, path...), fmt.Sprintf("[%d]", i)), x)
			}
		case nil:
			out = append(out, Scalar{Path: path})
		default:
			out = append(out, Scalar{Path: path, Value: fmt.Sprint(v)})
		}
	}
	walk(nil, doc)
	return out
}

// fluxReference is a variable in the form this estate writes its
// substitutions in: ${NAME}, the name in capitals. Flux's own grammar takes
// more (lower case, defaults); none of it is written here, and reading it
// would change what the guards that ask this refuse.
var fluxReference = regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]*)\}`)

// FluxVariables is every variable a document's values ask Flux to fill in,
// each once, in the order first asked. Flux substitutes into the text of
// what it applies, so the names are read from the text of each value.
func FluxVariables(doc any) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range Scalars(doc) {
		for _, m := range fluxReference.FindAllStringSubmatch(s.Value, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	return out
}
