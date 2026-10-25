package rules.root_paths_test

import data.rules.root_paths
import rego.v1

file(path, expr) := [{"path": path, "contents": {"locals": [{"x": expr}]}}]

found(msgs) if {
	some _ in msgs
}

test_every_way_up_but_the_top_is_found if {
	found(root_paths.deny) with input as file("one/two/a.tf", `${file("${path.module}/../tunnel-routes.json")}`)
	found(root_paths.deny) with input as file("one/two/a.tf", `${file("${path.module}/../../../x")}`)
	found(root_paths.deny) with input as file("one/two/three/a.tf", `${file("${path.module}/../../x")}`)
}

test_the_way_to_the_top_and_staying_inside_pass if {
	not found(root_paths.deny) with input as file("one/two/a.tf", `${file("${path.module}/../../management/tunnel-routes.json")}`)
	not found(root_paths.deny) with input as file("one/two/a.tf", `${file("${path.module}/files/x")}`)
	not found(root_paths.deny) with input as file("one/two/three/a.tf", `${file("${path.module}/../../../x")}`)
}
