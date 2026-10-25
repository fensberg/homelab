# The approved suppliers list is the estate's declaration of every outside
# party it may take delivery from. These rules are what make it a rule rather
# than a document.
#
# The incident behind it: an unpinned third-party image was proposed for a
# live cluster, with host networking, on a control-plane node, and nothing in
# this repository would have objected. The image was very likely harmless.
# "Very likely harmless" is the reasoning this estate exists to replace.
#
# Scope, stated because a guard nobody understands the edges of is worse than
# none: this catches what is COMMITTED. It cannot see a command pasted into a
# terminal, and it is not admission control. See scripts/approved-suppliers.yml.
package rules.suppliers

import data.lib
import rego.v1

list := [f.contents | some f in lib.at("scripts/approved-suppliers.yml")][0]

deny contains msg if {
	count(object.get(list, "registries", [])) == 0
	msg := "the suppliers list declares no registries, so every rule here proves nothing"
}

deny contains msg if {
	count(object.get(list, "endpoints", [])) == 0
	msg := "the suppliers list declares no endpoints, so every rule here proves nothing"
}

# Every reason has to be filled in. An entry with no reason is somebody
# widening the estate's supply chain without saying why, which is the review
# this file exists to force.

blank(entry, key) if trim_space($"{object.get(entry, key, "")}") == ""

deny contains msg if {
	some r in object.get(list, "registries", [])
	blank(r, "reason")
	msg := $"registry \"{r.host}\" is approved with no reason given"
}

deny contains msg if {
	some ch in object.get(list, "charts", [])
	blank(ch, "reason")
	msg := $"chart source \"{ch.url}\" is approved with no reason given"
}

deny contains msg if {
	some g in object.get(list, "endpoints", [])
	blank(g, "reason")
	msg := $"endpoint group \"{g.group}\" is approved with no reason given"
}

deny contains msg if {
	some e in object.get(list, "exemptions", [])
	blank(e, "reason")
	msg := $"exemption for \"{e.path}\" gives no reason"
}

deny contains msg if {
	some e in object.get(list, "exemptions", [])
	blank(e, "trigger")
	msg := $"exemption for \"{e.path}\" gives no trigger, so nothing says when it ends"
}

deny contains msg if {
	some e in object.get(list, "exemptions", [])
	not object.get(e, "issue", 0) > 0
	msg := $"exemption for \"{e.path}\" names no issue. File the work it defers and put the number in issue:, or fix the thing instead - a red check is not on its own a reason to write an excuse."
}

# An image must come from an approved registry and be pinned by digest.
#
# The digest half matters as much as the registry. A tag is a mutable pointer:
# the same name resolves to different software over time and git records
# nothing when it moves, so a tagged image from an approved registry is still
# an unreviewed change waiting to happen.

dir(path) := concat("/", array.slice(parts, 0, count(parts) - 1)) if parts := split(path, "/")

flux contains f if {
	some f in lib.under("clusters/")
	endswith(f.path, ".yaml")
}

ref_shape := `^[a-zA-Z0-9][a-zA-Z0-9./:@_-]+$`

# Every image a manifest names, as a field or as a line of a value that
# carries YAML of its own.
images contains [f.path, ref] if {
	some f in flux
	walk(f.contents, [path, ref])
	count(path) > 0
	path[count(path) - 1] == "image"
	is_string(ref)
	regex.match(ref_shape, ref)
}

images contains [f.path, m[1]] if {
	some f in flux
	walk(f.contents, [_, v])
	is_string(v)
	contains(v, "\n")
	some m in regex.find_all_string_submatch_n(`(?m)^\s*(?:-\s*)?image:\s*["']?([a-zA-Z0-9][a-zA-Z0-9./:@_-]+)["']?\s*$`, v, -1)
}

remote contains [path, ref] if {
	some [path, ref] in images
	contains(ref, "/")
}

overlay_pins(at, name) if {
	some k in lib.named("kustomization.yaml")
	k.path == $"{at}/kustomization.yaml"
	some image in object.get(k.contents, "images", [])
	image.name == name
	startswith(object.get(image, "digest", ""), "sha256:")
}

approved_registries contains r.host if {
	some r in object.get(list, "registries", [])
}

exempt contains e.path if {
	some e in object.get(list, "exemptions", [])
	e.rule == "digest-pinned-images"
}

unpinned contains [path, ref] if {
	some [path, ref] in remote
	not contains(ref, "@sha256:")
	name := split(ref, ":")[0]
	not overlay_pins(dir(path), name)
}

deny contains msg if {
	count(remote) == 0
	msg := "no image references found under clusters/, so this rule proves nothing"
}

deny contains msg if {
	some [path, ref] in remote
	host := split(ref, "/")[0]
	not host in approved_registries
	msg := $"{path}: image \"{ref}\" comes from \"{host}\", which is not an approved supplier. Add it to scripts/approved-suppliers.yml with a reason, or use a registry already approved."
}

deny contains msg if {
	some [path, ref] in unpinned
	not path in exempt
	msg := $"{path}: image \"{ref}\" is not pinned by digest. A tag is a mutable pointer - the same name is different software later, and git records nothing when it moves. If this genuinely cannot be pinned, declare it under exemptions: in scripts/approved-suppliers.yml with a reason and a trigger."
}

deny contains msg if {
	some path in exempt
	not path in {p | some [p, _] in unpinned}
	msg := $"{path} is exempted from digest pinning but no longer needs to be. Remove the exemption from scripts/approved-suppliers.yml - a stale excuse reads as a live gap."
}

# Charts decide what images get deployed, so an unapproved chart repository is
# an unapproved registry one level up.

# The repository's own address, from the source its Flux reads it from.
own := $"{m[1]}/{m[2]}" if {
	some f in lib.objects
	f.contents.kind == "GitRepository"
	f.contents.metadata.name == "flux-system"
	some m in regex.find_all_string_submatch_n(`^https://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?$`, f.contents.spec.url, 1)
}

deny contains msg if {
	not own
	msg := "the flux-system GitRepository names no repository on GitHub, so the repository's own address is unknown"
}

sources contains [f.path, u] if {
	some f in flux
	walk(f.contents, [path, u])
	count(path) > 0
	path[count(path) - 1] == "url"
	is_string(u)
	regex.match(`^(?:oci|https)://[^\s"']+$`, u)
}

charted contains [path, u] if {
	some [path, u] in sources
	not contains(u, own)
	not startswith(u, "oci://${RELEASE_REPOSITORY}-")
}

deny contains msg if {
	count(charted) == 0
	msg := "no chart sources found under clusters/, so this rule proves nothing"
}

deny contains msg if {
	some [path, u] in charted
	not u in {ch.url | some ch in object.get(list, "charts", [])}
	msg := $"{path}: chart source \"{u}\" is not an approved supplier. Add it to scripts/approved-suppliers.yml with a reason."
}

# A workflow may not invent its own egress endpoint.
#
# These were restated across fifteen allowlists in nine workflows, so no single
# place answered "what can this repository call out to". Adding one endpoint to
# one workflow was invisible to any review not already reading that file.

reaches contains [file, host] if {
	some file, doc in lib.workflows
	walk(doc, [path, v])
	count(path) > 0
	path[count(path) - 1] == "allowed-endpoints"
	is_string(v)
	some host in regex.split(`\s+`, trim_space(v))
	regex.match(`^[a-z0-9.-]+:\d+$`, host)
}

deny contains msg if {
	count(reaches) == 0
	msg := "no allowed-endpoints entries found, so this rule proves nothing"
}

deny contains msg if {
	some [file, host] in reaches
	not host in {h | some g in object.get(list, "endpoints", []); some h in object.get(g, "hosts", [])}
	msg := $"{file} reaches \"{host}\", which is not in the approved suppliers list. Declare it in scripts/approved-suppliers.yml with a reason rather than only in the workflow, so one place answers what this repository calls out to."
}

# Every repository pre-commit clones is a supplier, and must be declared.
#
# This was the delivery nobody was watching. Seven third-party repositories
# run on every commit with the developer's own privileges - not in a
# container, not in CI, on the machine holding the vault session. That is a
# better position than the container image which caused approved-suppliers.yml
# to exist, and none of it was declared anywhere.
#
# It stayed invisible because pre-commit clones into ~/.cache/pre-commit under
# generated names like `repornkulz89`. An eighth repository appearing there
# would look exactly like the seven that belong; the only reason this was ever
# looked at is that somebody saw a strange directory and asked.

hook_repos := [r | some f in lib.at(".pre-commit-config.yaml"); some r in object.get(f.contents, "repos", []); r.repo != "local"]

declared_hooks[h.repo] := object.get(h, "reason", "") if {
	some h in object.get(list, "hooks", [])
}

deny contains msg if {
	count(declared_hooks) == 0
	msg := "no commit hooks are declared as suppliers, so nothing checks what runs on every commit"
}

deny contains msg if {
	count(hook_repos) == 0
	msg := "no hook repositories were found in .pre-commit-config.yaml. Either the file changed shape or this rule now guards nothing, and passing on an empty set is how a check stops mattering."
}

deny contains msg if {
	some r in hook_repos
	not declared_hooks[r.repo]
	msg := $".pre-commit-config.yaml runs {r.repo} and scripts/approved-suppliers.yml does not declare it. That repository executes on every commit with the developer's own privileges. Add it under `hooks:` with a reason, or remove the hook."
}

deny contains msg if {
	some r in hook_repos
	reason := declared_hooks[r.repo]
	trim_space(reason) == ""
	msg := $"{r.repo} is declared with no reason. The reason is what a reviewer reads when deciding whether this supplier still belongs."
}

deny contains msg if {
	some repo, _ in declared_hooks
	not repo in {r.repo | some r in hook_repos}
	msg := $"scripts/approved-suppliers.yml declares {repo} and .pre-commit-config.yaml does not use it. A supplier nobody takes delivery from makes the list look longer than the exposure is."
}

# Every hook must be pinned. `rev:` is the whole supply-chain control here:
# a moving reference means the code that runs on the next commit is not the
# code anybody reviewed.

deny contains msg if {
	some r in hook_repos
	rev := trim_space($"{object.get(r, "rev", "")}")
	rev in {"", "main", "master", "HEAD"}
	msg := $"{r.repo} is pinned to \"{rev}\", which moves. The code that runs on the next commit would not be the code anybody reviewed."
}

# Every OpenTofu provider is a declared supplier, and every declared supplier
# is actually used.
#
# Providers are the most privileged deliveries this estate accepts: binaries
# downloaded at init time and executed locally, each handed a live credential -
# the hypervisor API token, the cluster PKI, the object storage keys. The
# hooks section of approved-suppliers.yml exists because seven repositories run
# on a developer's machine with a developer's privileges; this is the same
# shape at the estate's privilege, and it went undeclared until #230.
#
# Checked in both directions on purpose. One direction stops a provider being
# added to the HCL without anybody deciding to take delivery from that party;
# the other stops the list keeping an entry for a provider nobody uses, which
# is how an approval outlives the thing it approved.

# A provider is found by what tofu init locked for a root or a module: the
# providers it was initialised with, by source, the registry's host left
# off as the approved list writes them. A source changed in the HCL and not
# in the lock does not initialise, so the plans refuse it; changed in both,
# it is here.
required contains [path, short] if {
	some [path, source] in lib.provider_locks
	parts := split(source, "/")
	short := concat("/", array.slice(parts, 1, count(parts)))
}

approved_providers contains p.source if {
	some p in object.get(list, "providers", [])
}

deny contains msg if {
	some p in object.get(list, "providers", [])
	blank(p, "reason")
	msg := $"provider \"{p.source}\" is approved with no reason given"
}

deny contains msg if {
	count(approved_providers) == 0
	msg := "approved-suppliers.yml declares no providers, so this rule proves nothing"
}

deny contains msg if {
	count(required) == 0
	msg := "no provider is locked by any root or module, so this rule proves nothing"
}

deny contains msg if {
	some source in {s | some [_, s] in required}
	not source in approved_providers
	first := min({p | some [p, s] in required; s == source})
	msg := $"{first} locks the provider \"{source}\", which is not an approved supplier. A provider runs with this estate's credentials, so taking delivery from a new party is a decision rather than a line of HCL. Declare it in scripts/approved-suppliers.yml with a reason, in its own pull request."
}

deny contains msg if {
	some source in approved_providers
	not source in {s | some [_, s] in required}
	msg := $"scripts/approved-suppliers.yml approves the provider \"{source}\" and no provider lock locks it. Remove the entry - an approval that outlives the thing it approved is how a supplier list stops describing what the estate actually takes."
}
