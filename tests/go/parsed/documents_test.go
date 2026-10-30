package parsed

import (
	"slices"
	"strings"
	"testing"
)

// A unit's settings are read by section, a comment is no setting, and what
// its Exec settings run is read as commands, through a shell's script.
func TestAUnitIsReadAsItsSettingsAndWhatItRuns(t *testing.T) {
	settings, err := UnitOf(`# a drop-in
[Service]
ExecStartPost=/bin/sh -c 'ip rule list | grep -q 5200 || ip rule add pref 5200 lookup local'
; also a comment
Restart=always
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(settings) != 2 || settings[0].Section != "Service" || settings[1].Key != "Restart" || settings[1].Value != "always" {
		t.Errorf("settings read as %+v", settings)
	}
	commands, err := UnitCommands("drop-in", settings)
	if err != nil {
		t.Fatal(err)
	}
	var words []string
	for _, c := range commands {
		words = append(words, strings.Join(c.Words, " "))
	}
	if !slices.Contains(words, "ip rule add pref 5200 lookup local") || !slices.Contains(words, "grep -q 5200") {
		t.Errorf("the unit runs %q", words)
	}
	if _, err := UnitOf("Key=value\n"); err == nil {
		t.Error("a setting before any section was read")
	}
	if _, err := UnitOf("[Unit]\nnot a setting\n"); err == nil {
		t.Error("a line that is no setting was read as one")
	}
}

// A cloud-init document is read past its template statements, and what its
// runcmd runs is read as commands: a line, a list of words, and the script
// a list hands to a shell.
func TestACloudConfigIsReadAsWhatItRuns(t *testing.T) {
	doc, err := CloudConfigOf(`#cloud-config
{% if overlay %}
runcmd:
  - [apt-get, install, -y, tailscale]
  - [sh, -c, "tailscale up --auth-key='{{ key }}'"]
  - systemctl enable --now tailscaled
{% endif %}
`)
	if err != nil {
		t.Fatal(err)
	}
	commands, err := RunCommands("user-data", doc)
	if err != nil {
		t.Fatal(err)
	}
	var words []string
	for _, c := range commands {
		words = append(words, strings.Join(c.Words, " "))
	}
	want := []string{"apt-get install -y tailscale", "sh -c tailscale up --auth-key='{{ key }}'", "tailscale up --auth-key={{ key }}", "systemctl enable --now tailscaled"}
	if !slices.Equal(words, want) {
		t.Errorf("runcmd runs\n  %s\nwant\n  %s", strings.Join(words, "\n  "), strings.Join(want, "\n  "))
	}
}

// Every value of a document is read with where it is, a key never as a
// value; and the variables Flux would fill in are read from the values.
func TestScalarsAreValuesWithTheirPathsAndFluxVariablesAreReadFromThem(t *testing.T) {
	doc := map[string]any{
		"spec": map[string]any{
			"containers": []any{map[string]any{"image": "repo/x:v1", "args": []any{"--site=${SITE}", "${lower}", "${A}${B_2}"}}},
			"replicas":   2,
			"empty":      nil,
		},
		"${KEY}": "a key is not a value",
	}
	var at []string
	for _, s := range Scalars(doc) {
		at = append(at, s.At()+"="+s.Value)
	}
	want := []string{
		"${KEY}=a key is not a value",
		"spec.containers.[0].args.[0]=--site=${SITE}", "spec.containers.[0].args.[1]=${lower}", "spec.containers.[0].args.[2]=${A}${B_2}",
		"spec.containers.[0].image=repo/x:v1", "spec.empty=", "spec.replicas=2",
	}
	if !slices.Equal(at, want) {
		t.Errorf("scalars read as\n  %s\nwant\n  %s", strings.Join(at, "\n  "), strings.Join(want, "\n  "))
	}
	if key := Scalars(doc)[4].Key(); key != "image" {
		t.Errorf("an image's key read as %q", key)
	}
	if got := FluxVariables(doc); !slices.Equal(got, []string{"SITE", "A", "B_2"}) {
		t.Errorf("Flux variables read as %v", got)
	}
}
