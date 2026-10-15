package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"homelab/details/capacity"
	"homelab/details/flux"
)

// A change is tried the way the cluster will take it, as well as for room.
//
// Flux applies an object the site already has as an edit, and the API server
// refuses some edits outright: a PriorityClass's value and its preemption
// policy, among others, are fixed once it exists. One refused object stops
// Flux's whole layer, and every layer behind it, and nothing a pull request
// ran had asked the question - each check read what the manifests said, and
// the scheduler's trial made every object fresh. So the change's edits are
// applied, as a dry run and as Flux applies, over the site's own objects as
// its record keeps them, in a cluster of the same API server the site runs
// (#766).
//
// ONLY WHAT THE RECORD KEEPS AS IT IS. An edit is only tried over an object
// the record holds the refusable fields of. Today that is a PriorityClass,
// kept with its value, its default and its policy. A StorageClass is kept
// with its provisioner replaced and none of its parameters, so an edit
// tried over it would be refused for what the record left out; it is tried
// once the record keeps those.

// edited is each PriorityClass the change alters or adds, as the tree after
// it writes the class, that Flux would apply as an edit.
func edited(before, now asks) []capacity.Object {
	var names []string
	for name, class := range now.Classes {
		// One Flux replaces when an edit of it is refused is not an edit:
		// it is made again, which the cluster takes.
		if annotations, _ := dig(class, "metadata", "annotations").(map[string]any); annotations[flux.Force] == flux.Forced {
			continue
		}
		was, ok := before.Classes[name]
		x, _ := json.Marshal(was)
		y, _ := json.Marshal(class)
		if !ok || string(x) != string(y) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	out := make([]capacity.Object, 0, len(names))
	for _, name := range names {
		out = append(out, now.Classes[name])
	}
	return out
}

// refusedEdits applies each edit as a dry run over the objects the site has,
// and gives what the API server refused, said as flux.Refusal says it: the
// object, the field and the rule, and no value. Anything else that goes
// wrong is an error, and not a refusal of the change.
func refusedEdits(standing, edits []capacity.Object) ([]string, error) {
	dir, env, stop, err := trialCluster()
	if err != nil {
		return nil, err
	}
	defer stop()
	write := func(file string, objects []capacity.Object) (string, error) {
		raw, err := json.Marshal(capacity.Object{"apiVersion": "v1", "kind": "List", "items": objects})
		if err != nil {
			return "", err
		}
		path := filepath.Join(dir, file)
		return path, os.WriteFile(path, raw, 0o600)
	}
	path, err := write("kept.json", standing)
	if err != nil {
		return nil, err
	}
	if _, err := asking(asker, env, "apply", "--server-side", "--force-conflicts", "-f", path); err != nil {
		return nil, fmt.Errorf("the site's own objects could not be loaded into the cluster a change is tried in: %w", err)
	}
	var refused []string
	for i, edit := range edits {
		path, err := write(fmt.Sprintf("edit-%d.json", i), []capacity.Object{edit})
		if err != nil {
			return nil, err
		}
		_, err = asking(asker, env, "apply", "--server-side", capacity.DryRunOnServer, "--force-conflicts", "-f", path)
		switch {
		case err == nil:
		case strings.Contains(err.Error(), " is invalid"):
			refused = append(refused, "  "+flux.Refusal(err.Error()))
		default:
			return nil, fmt.Errorf("an edit could not be tried: %w", err)
		}
	}
	return refused, nil
}
