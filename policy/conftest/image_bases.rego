# An image that installs whatever version of a package the archive has starts
# from a base pinned by digest.
#
# hadolint's DL3008 wants every apt package pinned to an exact version, and
# each place it is silenced says why not: Debian's and Ubuntu's archives keep
# only the current version, so a pin stops existing the day a security update
# supersedes it, and until then it holds the image on the version that is not
# getting the update.
#
# That leaves the packages unpinned, which is only tolerable if it is all
# that is. So this holds what the comments rest on: the stage that runs
# `apt-get install` starts FROM an image named by its digest. What can then
# differ between two builds of one Dockerfile is the distribution's own
# updates to packages named here, on top of contents that cannot move - and
# not a base that was swapped under a tag.
package image_bases

import rego.v1

# A Dockerfile, as Conftest reads one, is its instructions in order, each
# with the stage it is in.
dockerfiles contains f if {
	some f in input
	is_array(f.contents)
	count(f.contents) > 0
	every i in f.contents {
		is_object(i)
		i.Cmd
	}
}

base(f, stage) := i.Value[0] if {
	some i in f.contents
	i.Cmd == "from"
	i.Stage == stage
}

installs contains [f, i] if {
	some f in dockerfiles
	some i in f.contents
	i.Cmd == "run"
	regex.match(`\bapt(-get)?\s+install\b`, concat(" ", i.Value))
}

deny contains msg if {
	some [f, i] in installs
	from := object.get({"b": base(f, i.Stage)}, "b", "")
	not contains(from, "@sha256:")
	msg := sprintf("%s installs packages at whatever version the archive has, in a stage that starts from %q, which is not pinned by digest. Unpinned packages are tolerable on a base that cannot move. On a tag, both move, and two builds of this file share nothing that is held.", [f.path, from])
}

deny contains msg if {
	count(installs) == 0
	msg := sprintf("found %d Dockerfile(s) and no package install, so nothing was checked", [count(dockerfiles)])
}
