package rules.image_bases_test

import data.rules.image_bases
import rego.v1

dockerfile(from) := {"path": "x/Dockerfile", "contents": [
	{"Cmd": "comment", "Stage": 0, "Value": ["FROM nothing@sha256:abc"]},
	{"Cmd": "from", "Stage": 0, "Value": ["golang:1.26@sha256:aaaa", "AS", "build"]},
	{"Cmd": "run", "Stage": 0, "Value": ["go build ."]},
	{"Cmd": "from", "Stage": 1, "Value": [from]},
	{"Cmd": "run", "Stage": 1, "Value": ["apt-get update && apt-get install -y curl"]},
]}

test_a_pinned_base_passes if {
	count(image_bases.deny) == 0 with input as [dockerfile("debian:13@sha256:bbbb")]
}

test_a_base_on_a_tag_is_refused if {
	some msg in image_bases.deny with input as [dockerfile("debian:13")]
	contains(msg, `starts from "debian:13", which is not pinned by digest`)
}

test_a_comment_naming_a_pin_is_not_one if {
	some msg in image_bases.deny with input as [{"path": "x/Dockerfile", "contents": [
		{"Cmd": "comment", "Stage": 0, "Value": ["FROM debian@sha256:abc"]},
		{"Cmd": "from", "Stage": 0, "Value": ["debian:13"]},
		{"Cmd": "run", "Stage": 0, "Value": ["apt install -y curl"]},
	]}]
	contains(msg, "which is not pinned by digest")
}

test_no_install_is_refused if {
	some msg in image_bases.deny with input as [{"path": "x/Dockerfile", "contents": [{"Cmd": "from", "Stage": 0, "Value": ["scratch"]}]}]
	contains(msg, "no package install")
}
