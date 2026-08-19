package main

# 1. Rule: The very first step of every job MUST be harden-runner
deny[msg] {
    job := input.jobs[job_name]
    first_step := job.steps[0]

    # Check if 'uses' exists on the first step, and if it starts with the required action
    not has_uses(first_step)
    msg := sprintf("Security Violation: Job '%v' must use step-security/harden-runner as its very first step.", [job_name])
}

deny[msg] {
    job := input.jobs[job_name]
    first_step := job.steps[0]

    has_uses(first_step)
    not startswith(first_step.uses, "step-security/harden-runner")
    msg := sprintf("Security Violation: Job '%v' must use step-security/harden-runner as its very first step.", [job_name])
}

# 2. Rule: Egress-policy MUST be set to 'block'
deny[msg] {
    job := input.jobs[job_name]
    first_step := job.steps[0]

    startswith(first_step.uses, "step-security/harden-runner")
    first_step.with["egress-policy"] != "block"

    msg := sprintf("Security Violation: Job '%v' is using harden-runner, but egress-policy is not explicitly set to 'block'.", [job_name])
}

# Helper to avoid nil pointer panics if a step runs a bash script instead of an action
has_uses(step) {
    step.uses
}
