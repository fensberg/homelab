# The cluster's asking fits what GitHub allows a caller with no token: sixty
# requests an hour from one address. More workflows or a shorter interval
# than that allows is refused here, not found out when GitHub starts refusing
# every question. What is asked is rules.scheduled_workflows.
package rules.scheduled_allowance

import data.lib
import rego.v1

name := "scheduled-workflows"

object_of(kind) := [f | some f in lib.objects; f.contents.kind == kind; f.contents.metadata.name == name]

scrape := object_of("ScrapeConfig")

where(objects) := concat(", ", sort({f.path | some f in objects}))

targets contains t if {
	some f in scrape
	some sc in object.get(f.contents.spec, "staticConfigs", [])
	some t in object.get(sc, "targets", [])
}

workflow_of := `/actions/workflows/([^/]+)/runs\?event=schedule&status=completed&per_page=1$`

asked contains m[1] if {
	some t in targets
	some m in regex.find_all_string_submatch_n(workflow_of, t, 1)
}

interval := object.get(scrape[0].contents.spec, "scrapeInterval", "")

allowed := 60

share := 2

deny contains msg if {
	count(scrape) != 1
	msg := $"{count(scrape)} ScrapeConfig named {name} were found, so what the asking costs GitHub's allowance cannot be read"
}

deny contains msg if {
	count(scrape) == 1
	not regex.match(`^\d+m$`, interval)
	msg := $"{where(scrape)} asks every \"{interval}\", which is not a number of minutes, so what it costs GitHub's allowance cannot be worked out"
}

deny contains msg if {
	regex.match(`^\d+m$`, interval)
	every_minutes := to_number(trim_suffix(interval, "m"))
	an_hour := floor((count(asked) * 60) / every_minutes)
	an_hour > allowed / share
	msg := $"{where(scrape)} asks GitHub {an_hour} times an hour: {count(asked)} workflows every {every_minutes} minutes. A caller with no token is allowed {allowed} from one address, and this may use half. Ask less often."
}
