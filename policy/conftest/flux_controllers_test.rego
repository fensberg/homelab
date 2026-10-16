package flux_controllers_test

import data.flux_controllers
import rego.v1

names := ["helm-controller", "kustomize-controller", "notification-controller", "source-controller"]

components := [{"path": "clusters/core/flux-system/gotk-components.yaml", "contents": {"kind": "Deployment", "spec": {"template": {"spec": {"containers": [{"image": sprintf("ghcr.io/fluxcd/%s:v1.2.3", [n])}]}}}}} | some n in names]

overlay(pinned) := {"path": "clusters/core/flux-system/kustomization.yaml", "contents": {"images": [{"name": sprintf("ghcr.io/fluxcd/%s", [n]), "digest": "sha256:aaaa"} | some n in pinned]}}

test_every_controller_pinned_passes if {
	count(flux_controllers.deny) == 0 with input as array.concat(components, [overlay(names)])
}

test_a_controller_the_overlay_does_not_name_is_refused if {
	some msg in flux_controllers.deny with input as array.concat(components, [overlay(["helm-controller", "kustomize-controller", "notification-controller"])])
	contains(msg, "ships ghcr.io/fluxcd/source-controller and the kustomization beside it does not pin it")
}

test_a_name_with_no_digest_is_not_a_pin if {
	o := {"path": "clusters/core/flux-system/kustomization.yaml", "contents": {"images": [{"name": sprintf("ghcr.io/fluxcd/%s", [n]), "newTag": "v1.2.3"} | some n in names]}}
	count(flux_controllers.deny) == 4 with input as array.concat(components, [o])
}

test_too_few_controllers_is_refused if {
	some msg in flux_controllers.deny with input as [components[0], overlay(names)]
	contains(msg, "found 1 controller image(s)")
}
