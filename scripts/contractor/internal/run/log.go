package run

import (
	"time"

	"homelab/details/console"
)

// The contractor's output is the shared console's; these names are how the
// phases have always called it.

func WritePhase(name, description string) { console.Phase(name, description) }
func Elapsed(d time.Duration)             { console.Elapsed(d) }
func Info(msg string)                     { console.Info(msg) }
func Ok(msg string)                       { console.Ok(msg) }
func Warn(msg string)                     { console.Warn(msg) }
func Fail(msg string)                     { console.Fail(msg) }
