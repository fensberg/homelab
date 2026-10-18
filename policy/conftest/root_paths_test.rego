package rules.root_paths_test

import data.rules.root_paths
import rego.v1

file(path, expr) := [{"path": path, "contents": {"locals": [{"x": expr}]}}]

found(path, expr) if {
	some _ in root_paths.deny with input as file(path, expr)
}

test_every_way_up_but_the_top_is_found if {
	found("one/two/a.tf", `${file("${path.module}/../tunnel-routes.json")}`)
	found("one/two/a.tf", `${file("${path.module}/../../../x")}`)
	found("one/two/three/a.tf", `${file("${path.module}/../../x")}`)
}

test_the_way_to_the_top_and_staying_inside_pass if {
	not found("one/two/a.tf", `${file("${path.module}/../../management/tunnel-routes.json")}`)
	not found("one/two/a.tf", `${file("${path.module}/files/x")}`)
	not found("one/two/three/a.tf", `${file("${path.module}/../../../x")}`)
}
