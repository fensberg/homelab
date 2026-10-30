package parsed

import (
	"slices"
	"strings"
	"testing"
)

const fixturePlaybook = `
- name: prepare
  hosts: hypervisors
  vars:
    tag: "tag:example"
  pre_tasks:
    - name: first
      ansible.builtin.debug:
        msg: hello
  tasks:
    - name: free-form
      ansible.builtin.command: ip rule add pref 5200 lookup local
    - name: by cmd
      ansible.builtin.command:
        cmd: "systemctl restart {{ unit }}"
    - name: by argv
      ansible.builtin.command:
        argv: [sh, -c, "tailscale up --accept-routes --advertise-tags='{{ tag }}'"]
    - name: guarded
      block:
        - name: inside
          ansible.builtin.shell: "echo {{ item.vnet }} | tee -a /etc/x"
          loop: "{{ zones }}"
          when: zones | length > 0 and item.enabled is defined
      rescue:
        - name: rescued
          ansible.builtin.fail:
            msg: "{{ 'quoted.name' }}"
    - name: drop-in
      ansible.builtin.copy:
        dest: /etc/systemd/system/x.service.d/10-y.conf
        content: |
          [Service]
          ExecStartPost=/bin/sh -c 'ip rule add pref 5200 lookup local'
    - name: user-data
      ansible.builtin.copy:
        dest: /var/lib/vz/snippets/vendor.yml
        content: |
          #cloud-config
          runcmd:
            - [tailscale, up]
    - name: listed
      ansible.posix.sysctl:
        name: "{{ item }}"
        value: "1"
      loop:
        - net.ipv6.conf.all.disable_ipv6
  handlers:
    - name: last
      ansible.builtin.systemd:
        name: tailscaled
`

func playbook(t *testing.T) Play {
	t.Helper()
	tree := Empty(t)
	tree.Write(t, "play.yml", fixturePlaybook)
	tree.Index(t)
	plays := tree.Playbook(t, "play.yml")
	if len(plays) != 1 {
		t.Fatalf("%d plays were read, and one is written", len(plays))
	}
	return plays[0]
}

// Every task is read, in the order written, from each section of the play
// and from inside a block, its rescue and its always, with the module it
// runs and what it is given.
func TestEveryTaskOfAPlayIsReadWithItsModule(t *testing.T) {
	p := playbook(t)
	var names []string
	for _, task := range p.Tasks {
		names = append(names, task.Name+"="+task.Module)
	}
	want := []string{
		"first=ansible.builtin.debug", "free-form=ansible.builtin.command", "by cmd=ansible.builtin.command",
		"by argv=ansible.builtin.command", "inside=ansible.builtin.shell", "rescued=ansible.builtin.fail",
		"drop-in=ansible.builtin.copy", "user-data=ansible.builtin.copy", "listed=ansible.posix.sysctl", "last=ansible.builtin.systemd",
	}
	if !slices.Equal(names, want) {
		t.Errorf("tasks read as\n  %s\nwant\n  %s", strings.Join(names, "\n  "), strings.Join(want, "\n  "))
	}
	if p.Hosts != "hypervisors" || p.Vars["tag"] != "tag:example" {
		t.Errorf("the play read as hosts %q, vars %v", p.Hosts, p.Vars)
	}
	listed := p.Tasks[8]
	if !listed.Is("sysctl") || len(listed.Loop) != 1 || listed.Args["value"] != "1" {
		t.Errorf("a module's arguments and a written loop were read as %+v", listed)
	}
	if inside := p.Tasks[4]; inside.LoopExpr != "{{ zones }}" || len(inside.When) != 1 {
		t.Errorf("a worked-out loop and a condition were read as %q, %v", inside.LoopExpr, inside.When)
	}
}

// A task's commands are its words, however the command is written - free,
// as cmd, as argv - and a script handed to a shell is read as the commands
// in it. A task that runs no command runs none.
func TestATasksCommandsAreReadHoweverTheyAreWritten(t *testing.T) {
	p := playbook(t)
	words := func(i int) []string {
		cs, err := p.Tasks[i].Commands()
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range cs {
			out = append(out, strings.Join(c.Words, " "))
		}
		return out
	}
	for i, want := range map[int][]string{
		1: {"ip rule add pref 5200 lookup local"},
		2: {"systemctl restart {{ unit }}"},
		3: {"sh -c tailscale up --accept-routes --advertise-tags='{{ tag }}'", "tailscale up --accept-routes --advertise-tags={{ tag }}"},
		4: {"echo {{ item.vnet }}", "tee -a /etc/x"},
		8: nil,
	} {
		if got := words(i); !slices.Equal(got, want) {
			t.Errorf("%s runs %q, want %q", p.Tasks[i].Name, got, want)
		}
	}
}

// What a task refers to is read from its expressions and its conditions:
// a variable or a path into one, and never a filter, a test, a keyword or
// a word inside a quoted string.
func TestWhatATaskUsesIsReadFromItsExpressions(t *testing.T) {
	p := playbook(t)
	inside, rescued := p.Tasks[4], p.Tasks[5]
	for _, name := range []string{"item.vnet", "item", "zones", "item.enabled"} {
		if !inside.Uses(name) {
			t.Errorf("the task does not use %s", name)
		}
	}
	for _, name := range []string{"length", "defined", "vnet", "tee"} {
		if inside.Uses(name) {
			t.Errorf("the task was read as using %s", name)
		}
	}
	if rescued.Uses("quoted") {
		t.Error("a word inside a quoted string was read as a variable")
	}
	if got := JinjaExpressionNames("overlay_auth_key | length > 0"); !slices.Equal(got, []string{"overlay_auth_key"}) {
		t.Errorf("a condition's names are %v", got)
	}
}

// A play with a task that runs no module, or two, is refused: either is a
// task Ansible would refuse, and reading it as something would guess.
func TestATaskThatRunsNoOneModuleIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"two modules": "- hosts: all\n  tasks:\n    - name: x\n      ansible.builtin.debug: {msg: a}\n      ansible.builtin.fail: {msg: b}\n",
		"no module":   "- hosts: all\n  tasks:\n    - name: x\n      register: y\n",
	} {
		tree := Empty(t)
		tree.Write(t, "play.yml", body)
		tree.Index(t)
		if !refuses(t, func(tb testing.TB) { tree.Playbook(tb, "play.yml") }) {
			t.Errorf("%s: the playbook was read", name)
		}
	}
}

// What a play runs is every task's command, and what the units and the
// cloud-init documents it writes will run.
func TestWhatAPlayRunsIncludesWhatItInstallsToRun(t *testing.T) {
	commands, err := playbook(t).Commands()
	if err != nil {
		t.Fatal(err)
	}
	var words []string
	for _, c := range commands {
		words = append(words, strings.Join(c.Words, " "))
	}
	for _, want := range []string{"ip rule add pref 5200 lookup local", "systemctl restart {{ unit }}", "tailscale up", "echo {{ item.vnet }}"} {
		if !slices.Contains(words, want) {
			t.Errorf("the play does not run %q; it runs %q", want, words)
		}
	}
	count := 0
	for _, w := range words {
		if w == "ip rule add pref 5200 lookup local" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("the rule is run %d times, and it is run by a task and by the unit a task writes", count)
	}
}
