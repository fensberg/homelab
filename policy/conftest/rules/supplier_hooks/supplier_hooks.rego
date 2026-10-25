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
package rules.supplier_hooks

import data.lib
import rego.v1

list := [f.contents | some f in lib.at("scripts/approved-suppliers.yml")][0]

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

deny contains msg if {
	some r in hook_repos
	reason := declared_hooks[r.repo]
	trim_space(reason) == ""
	msg := $"{r.repo} is declared with no reason. The reason is what a reviewer reads when deciding whether this supplier still belongs."
}
