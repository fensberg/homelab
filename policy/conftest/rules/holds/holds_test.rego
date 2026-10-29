package rules.holds_test

import data.rules.holds
import rego.v1

driver := {"path": "clusters/core/storage-driver.yaml", "contents": {"kind": "HelmRelease", "metadata": {"name": "driver"}, "spec": {"chart": {"spec": {"chart": "proxmox-csi-plugin"}}, "values": {"storageClass": [{"name": "kept"}]}}}}

claim(path, name, class) := {"path": path, "contents": {"kind": "PersistentVolumeClaim", "metadata": {"name": name}, "spec": {"storageClassName": class}}}

core_holds(entries) := {"path": "clusters/core/holds.json", "contents": {"holds": entries}}

app(name, declaration) := {"path": $"modules/applications/{name}/application.json", "contents": declaration}

entry(held_by, lives_on) := {"what": "a thing", "lifetime": "site", "lives_on": lives_on, "held_by": held_by}

good := [
	driver,
	claim("clusters/core/data.yaml", "data", "kept"),
	core_holds([entry("PersistentVolumeClaim/data", "node")]),
	claim("modules/applications/game/base/world.yaml", "world", "local"),
	app("game", {"holds": [object.union(entry("PersistentVolumeClaim/world", "machine"), {"copy": {"under": "worlds"}})], "secrets": {"backup": {"KEY": {"storage": "access_key_id"}}}}),
]

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_everything_declared_where_it_lives_passes if {
	count(holds.deny) == 0 with input as good
}

test_storage_nobody_declares_is_refused if {
	denies(holds.deny, "declares storage (PersistentVolumeClaim/extra) and nothing says what it holds. core owns it") with input as array.concat(good, [claim("clusters/core/extra.yaml", "extra", "kept")])
}

test_an_applications_storage_is_its_own_to_declare if {
	declared_by_core := [driver, claim("modules/applications/game/base/world.yaml", "world", "local"), core_holds([entry("PersistentVolumeClaim/world", "machine")]), app("game", {})]
	denies(holds.deny, "game owns it") with input as declared_by_core
}

test_a_hold_of_nothing_is_refused if {
	denies(holds.deny, "core says it holds something in PersistentVolumeClaim/gone") with input as array.concat(good, [core_holds([entry("PersistentVolumeClaim/data", "node"), entry("PersistentVolumeClaim/gone", "node")])])
}

test_a_volume_said_to_live_where_it_does_not_is_refused if {
	moved := [driver, claim("clusters/core/data.yaml", "data", "local"), core_holds([entry("PersistentVolumeClaim/data", "node")])]
	denies(holds.deny, "which lives on a machine, and core declares it lives on a node") with input as moved
}

test_a_bucket_key_with_no_copy_is_refused if {
	uncopied := [driver, claim("modules/applications/game/base/world.yaml", "world", "local"), app("game", {"holds": [entry("PersistentVolumeClaim/world", "machine")], "secrets": {"backup": {"KEY": {"storage": "access_key_id"}}}})]
	denies(holds.deny, "modules/applications/game is given a key to a bucket and holds nothing with a copy") with input as uncopied
}

test_a_claim_template_and_a_database_cluster_keep_storage if {
	templated := {"path": "clusters/core/set.yaml", "contents": {"kind": "StatefulSet", "metadata": {"name": "set"}, "spec": {"volumeClaimTemplates": [{"spec": {"storageClassName": "kept"}}]}}}
	denies(holds.deny, "declares storage (StatefulSet/set)") with input as [driver, templated]
	cluster := {"path": "clusters/core/db.yaml", "contents": {"kind": "Cluster", "metadata": {"name": "db"}, "spec": {"storage": {"storageClass": "kept"}}}}
	denies(holds.deny, "declares storage (Cluster/db)") with input as [driver, cluster]
}

test_nothing_to_read_is_refused if {
	denies(holds.deny, "found nothing in the repository that declares storage") with input as []
	denies(holds.deny, "the storage driver's release declares no class") with input as []
}
