// Standard details: the building blocks every program in this repository may
// use, drawn once and referenced rather than redrawn.
//
// The name is the architectural term. A standard detail is a drawing reused
// across every set of plans that needs it - how a wall meets a floor, how a
// door is flashed - so no architect draws it again and no two drawings of it
// can disagree. That is the job: anything two programs here both need lives in
// this module, once.
//
// ZERO DEPENDENCIES, AND IT MUST STAY THAT WAY. Every program imports this
// through a local replace, including scripts/contractor, whose own zero-
// dependency property is load-bearing: no go.sum to cache, nothing for Trivy's
// gomod scan to find, nothing for Dependabot to open a pull request against.
// A third-party import here would reach all of them at once.
//
// It exists because the answer to "where is the repository root" was written
// nine times across three modules, and because the only shared home anything
// had was the contractor - the one program that can destroy the estate, and
// the wrong thing for every other program's build to depend on.
module homelab/details

go 1.26
