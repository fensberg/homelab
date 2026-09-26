// Package procenv builds the environment a child process runs with, when the
// program configures a tool through a namespace of environment variables.
//
// Appending the program's settings to os.Environ() looks like configuring the
// tool and is not: the child also gets every variable in that namespace the
// parent happened to hold. rclone reads RCLONE_* and the S3 backend reads
// AWS_*, so an inherited RCLONE_VERSION stops rclone starting, and an
// inherited AWS_PROFILE or AWS_SESSION_TOKEN quietly changes whose credentials
// a state backend uses (#486). A program that owns a namespace must own all
// of it.
package procenv

import "strings"

// Namespaces are the variable prefixes a tool reads its configuration from.
// When a program sets any variable in one, it owns the whole namespace.
var Namespaces = []string{"RCLONE_", "AWS_"}

// With is base with every variable dropped that sits in a namespace extra
// sets, followed by extra. A namespace extra does not touch is left alone.
func With(base, extra []string) []string {
	owned := map[string]bool{}
	for _, kv := range extra {
		for _, ns := range Namespaces {
			if strings.HasPrefix(kv, ns) {
				owned[ns] = true
			}
		}
	}
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		keep := true
		for ns := range owned {
			if strings.HasPrefix(kv, ns) {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, kv)
		}
	}
	return append(out, extra...)
}
