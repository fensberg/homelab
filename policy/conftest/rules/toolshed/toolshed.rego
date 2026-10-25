# Every program is built into one ignored directory.
#
# The taskfile builds rather than using `go run`, deliberately: `go run`'s
# wrapper swallows SIGINT, which orphaned real infrastructure once. The cost of
# that choice used to be a binary beside each program's source and one ignore
# rule per program - individually forgettable, and two were forgotten. The
# canary never had one, and survey's was added after the binary was already
# tracked, so it never took effect and 3.6MB of compiled Go sat on main until a
# check went looking.
#
# One directory cannot be forgotten for a program nobody thought about. These
# assert the arrangement holds rather than trusting that the next person
# follows it.
package rules.toolshed

import data.lib
import rego.v1

taskfile := [f | some f in lib.at("taskfile.yml")]

targets contains m[1] if {
	some f in taskfile
	walk(f.contents, [_, v])
	is_string(v)
	some m in regex.find_all_string_submatch_n(`go build -C \S+ -o (\S+)`, v, -1)
	m[1] != "/dev/null"
}

ignored contains entry.Value if {
	some f in lib.at(".gitignore")
	some entry in f.contents
	entry.Kind == "Path"
}

deny contains msg if {
	some target in targets
	not contains(target, "toolshed/")
	msg := $"taskfile.yml builds a binary to \"{target}\", outside the toolshed. Then it needs its own ignore rule, which is the arrangement that let a binary reach main. Build into toolshed/ instead."
}

deny contains msg if {
	count(targets) == 0
	msg := "no builds found in taskfile.yml, so this guards nothing - and passing on an empty set is how a check stops mattering"
}

deny contains msg if {
	not "toolshed/" in ignored
	not "toolshed" in ignored
	msg := "toolshed/ is not ignored, so every build leaves untracked binaries and the next `git add -A` commits them"
}
