# A job is bounded. Without timeout-minutes it inherits GitHub's six hours, in
# which a stuck job keeps calling whatever it can reach, metered vendors too.
package rules.job_timeout

import data.lib
import rego.v1

deny contains msg if {
	count(lib.jobs) == 0
	msg := "found no jobs in any workflow, so no job was checked for a bound"
}

deny contains msg if {
	some [file, name, job] in lib.jobs
	not bounded(job)
	msg := $"{file}: job \"{name}\" declares no timeout-minutes, so it inherits GitHub's default of 360 - six hours in which a stuck job keeps calling whatever it can reach, including metered vendors. Give it a bound comfortably above its normal runtime."
}

bounded(job) if {
	is_number(job["timeout-minutes"])
	job["timeout-minutes"] > 0
}
