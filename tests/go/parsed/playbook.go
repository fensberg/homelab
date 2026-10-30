package parsed

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// Play is one play of an Ansible playbook: what it is called, the hosts it
// runs on, the variables it sets, and every task it runs.
type Play struct {
	Name  string
	Hosts string
	Vars  map[string]any
	// Tasks is every task the play runs, in the order written: its
	// pre_tasks, tasks, post_tasks and handlers, and each task inside a
	// block, its rescue and its always.
	Tasks []Task
}

// Task is one task, as Ansible reads it.
type Task struct {
	Name string
	// Module is the module the task runs, by the name it is written with:
	// ansible.builtin.copy, ansible.posix.sysctl.
	Module string
	// Args is what the module is given. A module given one line rather
	// than a mapping - a free-form command - has it under "_raw".
	Args map[string]any
	// Loop is the items the task loops over when they are written out,
	// and LoopExpr the expression when they are worked out.
	Loop     []any
	LoopExpr string
	// When is the conditions the task runs under.
	When []string
	// Fields is the whole task, for what is not named above.
	Fields map[string]any
}

// taskKeys is every key of a task that is not the module it runs.
var taskKeys = map[string]bool{
	"name": true, "when": true, "loop": true, "loop_control": true, "with_items": true, "register": true,
	"changed_when": true, "failed_when": true, "tags": true, "become": true, "become_user": true,
	"run_once": true, "delegate_to": true, "check_mode": true, "no_log": true, "environment": true,
	"until": true, "retries": true, "delay": true, "notify": true, "vars": true, "ignore_errors": true,
	"listen": true, "block": true, "rescue": true, "always": true, "args": true,
}

// Playbook is every play of an Ansible playbook in the tree.
func (r *Tree) Playbook(t testing.TB, rel string) []Play {
	t.Helper()
	var doc []map[string]any
	r.YAMLInto(t, rel, &doc)
	plays, err := playsOf(doc)
	if err != nil {
		t.Fatalf("%s is not a playbook this reads: %v", rel, err)
	}
	return plays
}

func playsOf(doc []map[string]any) ([]Play, error) {
	var plays []Play
	for _, p := range doc {
		play := Play{}
		play.Name, _ = p["name"].(string)
		play.Hosts, _ = p["hosts"].(string)
		play.Vars, _ = p["vars"].(map[string]any)
		for _, section := range []string{"pre_tasks", "tasks", "post_tasks", "handlers"} {
			list, _ := p[section].([]any)
			tasks, err := tasksOf(list)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", section, err)
			}
			play.Tasks = append(play.Tasks, tasks...)
		}
		plays = append(plays, play)
	}
	return plays, nil
}

func tasksOf(list []any) ([]Task, error) {
	var out []Task
	for _, item := range list {
		fields, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("a task is a %T, not a mapping", item)
		}
		if _, isBlock := fields["block"]; isBlock {
			for _, part := range []string{"block", "rescue", "always"} {
				inner, _ := fields[part].([]any)
				tasks, err := tasksOf(inner)
				if err != nil {
					return nil, err
				}
				out = append(out, tasks...)
			}
			continue
		}
		task := Task{Fields: fields}
		task.Name, _ = fields["name"].(string)
		var modules []string
		for k := range fields {
			if !taskKeys[k] {
				modules = append(modules, k)
			}
		}
		if len(modules) != 1 {
			sort.Strings(modules)
			return nil, fmt.Errorf("task %q runs %d modules (%s), and a task runs one", task.Name, len(modules), strings.Join(modules, ", "))
		}
		task.Module = modules[0]
		switch a := fields[task.Module].(type) {
		case map[string]any:
			task.Args = a
		case string:
			task.Args = map[string]any{"_raw": a}
		case nil:
			task.Args = map[string]any{}
		default:
			return nil, fmt.Errorf("task %q gives %s a %T", task.Name, task.Module, a)
		}
		if extra, ok := fields["args"].(map[string]any); ok {
			for k, v := range extra {
				task.Args[k] = v
			}
		}
		switch l := fields["loop"].(type) {
		case []any:
			task.Loop = l
		case string:
			task.LoopExpr = l
		}
		switch w := fields["when"].(type) {
		case string:
			task.When = []string{w}
		case []any:
			for _, c := range w {
				if s, ok := c.(string); ok {
					task.When = append(task.When, s)
				}
			}
		}
		out = append(out, task)
	}
	return out, nil
}

// short is a module's name without its collection: copy for
// ansible.builtin.copy.
func short(module string) string { return module[strings.LastIndex(module, ".")+1:] }

// Is says whether the task runs a module, named without its collection.
func (task Task) Is(module string) bool { return short(task.Module) == module }

// Commands is every command the task runs: a command or shell task's
// command, free-form, as cmd, or as argv, with each sh -c or bash -c in it
// read as the script it runs. A task that runs no command runs none.
func (task Task) Commands() ([]Command, error) {
	if !task.Is("command") && !task.Is("shell") && !task.Is("raw") {
		return nil, nil
	}
	if argv, ok := task.Args["argv"].([]any); ok {
		var words []string
		for _, w := range argv {
			words = append(words, fmt.Sprint(w))
		}
		return ExpandShells(task.Name, []Command{{Words: words, Assigns: map[string]string{}}})
	}
	line, _ := task.Args["cmd"].(string)
	if line == "" {
		line, _ = task.Args["_raw"].(string)
	}
	commands, err := TemplateCommands(task.Name, line)
	if err != nil {
		return nil, err
	}
	return ExpandShells(task.Name, commands)
}

// shells is how a command hands a script to a shell.
var shells = map[string]bool{"sh": true, "bash": true, "/bin/sh": true, "/bin/bash": true, "/usr/bin/sh": true, "/usr/bin/bash": true}

// ExpandShells is the commands given, and for each that hands a script to a
// shell (sh -c '...'), every command in that script after it, as deep as
// they go.
func ExpandShells(name string, commands []Command) ([]Command, error) {
	var out []Command
	for _, c := range commands {
		out = append(out, c)
		if len(c.Words) < 3 || !shells[c.Words[0]] || c.Words[1] != "-c" {
			continue
		}
		inner, err := TemplateCommands(name, c.Words[2])
		if err != nil {
			return nil, err
		}
		expanded, err := ExpandShells(name, inner)
		if err != nil {
			return nil, err
		}
		out = append(out, expanded...)
	}
	return out, nil
}

// Uses says whether anything the task is written with - its module's
// arguments, its conditions, its loop - refers to a variable by that name
// or to a part of it: Uses("item.vnet") holds for {{ item.vnet }}.
func (task Task) Uses(name string) bool {
	for _, s := range stringsIn(task.Fields) {
		for _, n := range JinjaNames(s) {
			if n == name || strings.HasPrefix(n, name+".") {
				return true
			}
		}
	}
	// A condition is an expression with no braces around it.
	for _, w := range task.When {
		for _, n := range JinjaExpressionNames(w) {
			if n == name || strings.HasPrefix(n, name+".") {
				return true
			}
		}
	}
	return false
}

// stringsIn is every string a decoded document holds as a value.
func stringsIn(v any) []string {
	var out []string
	switch v := v.(type) {
	case string:
		out = append(out, v)
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, stringsIn(v[k])...)
		}
	case []any:
		for _, x := range v {
			out = append(out, stringsIn(x)...)
		}
	}
	return out
}

var (
	jinjaBraces = regexp.MustCompile(`\{\{(.*?)\}\}`)
	jinjaQuoted = regexp.MustCompile(`'[^']*'|"[^"]*"`)
	jinjaName   = regexp.MustCompile(`(\|\s*)?([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)`)
	jinjaWords  = map[string]bool{"and": true, "or": true, "not": true, "is": true, "in": true, "if": true, "else": true,
		"true": true, "false": true, "none": true, "True": true, "False": true, "None": true, "defined": true, "undefined": true}
)

// JinjaNames is every variable the {{ expressions }} in a string refer to.
func JinjaNames(s string) []string {
	var out []string
	for _, m := range jinjaBraces.FindAllStringSubmatch(s, -1) {
		out = append(out, JinjaExpressionNames(m[1])...)
	}
	return out
}

// JinjaExpressionNames is every variable a Jinja expression refers to: a
// name or a dotted path to one, and never a filter, a test, a keyword or a
// word inside a quoted string.
func JinjaExpressionNames(expr string) []string {
	expr = jinjaQuoted.ReplaceAllString(expr, `""`)
	var out []string
	for _, m := range jinjaName.FindAllStringSubmatchIndex(expr, -1) {
		if m[2] >= 0 {
			continue // after a |: a filter
		}
		name := expr[m[4]:m[5]]
		before := strings.TrimRight(expr[:m[0]], " ")
		if jinjaWords[name] || before == "is" || strings.HasSuffix(before, " is") {
			continue
		}
		if m[1] < len(expr) && expr[m[1]] == '(' {
			continue // a call: a function or a test, not a variable
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// Writes is the file a copy task writes and what it writes there, when it
// writes it from content written in the task.
func (task Task) Writes() (dest, content string, ok bool) {
	if !task.Is("copy") {
		return "", "", false
	}
	dest, _ = task.Args["dest"].(string)
	content, ok = task.Args["content"].(string)
	return dest, content, ok && dest != ""
}

// systemdUnits is where a unit or a drop-in for one is written.
const systemdUnits = "/etc/systemd/system/"

// Commands is every command the play runs or installs to be run: each
// task's commands, the Exec lines of every systemd unit it writes, and the
// runcmd of every cloud-init document it writes.
func (p Play) Commands() ([]Command, error) {
	var out []Command
	for _, task := range p.Tasks {
		commands, err := task.Commands()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", task.Name, err)
		}
		out = append(out, commands...)
		dest, content, ok := task.Writes()
		switch {
		case !ok:
		case strings.HasPrefix(dest, systemdUnits):
			settings, err := UnitOf(content)
			if err != nil {
				return nil, fmt.Errorf("%s writes %s, which is not a unit: %w", task.Name, dest, err)
			}
			commands, err := UnitCommands(task.Name, settings)
			if err != nil {
				return nil, err
			}
			out = append(out, commands...)
		case strings.HasPrefix(strings.TrimSpace(content), "#cloud-config"):
			doc, err := CloudConfigOf(content)
			if err != nil {
				return nil, fmt.Errorf("%s writes a cloud-init document that is not YAML: %w", task.Name, err)
			}
			commands, err := RunCommands(task.Name, doc)
			if err != nil {
				return nil, err
			}
			out = append(out, commands...)
		}
	}
	return out, nil
}
