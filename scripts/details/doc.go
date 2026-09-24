// Package details holds the standard details: building blocks every program
// in this repository may import, drawn once and referenced rather than
// redrawn.
//
// A standard detail, in architecture, is a drawing reused across every set of
// plans that needs it, so no two drawings of it can disagree. That is the job
// of each package below this one. Anything two programs here both need belongs
// in one of them, and tests/go/repo/building_blocks_test.go declares what has
// not moved here yet.
//
// This root package has no code. It exists so the module has something at its
// root to build, which is how the validate lane checks every module - and so
// the reason the module exists is written where somebody opening it will look.
package details
