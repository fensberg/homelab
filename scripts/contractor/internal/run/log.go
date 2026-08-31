package run

import (
	"fmt"
	"strings"
	"time"
)

const (
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
	colorReset  = "\033[0m"
)

var phaseNum int

// WritePhase prints the banner between phases. Numbering is process-global,
// matching the original script's behaviour of counting phases as they run
// rather than their fixed position in the full sequence - so `-from Compute`
// prints "PHASE 1: COMPUTE", not "PHASE 5".
func WritePhase(name, description string) {
	phaseNum++
	bar := strings.Repeat("=", 72)
	fmt.Println()
	fmt.Println(colorCyan + bar + colorReset)
	fmt.Printf("%s PHASE %d : %s%s\n", colorCyan, phaseNum, strings.ToUpper(name), colorReset)
	fmt.Printf("%s %s%s\n", colorGray, description, colorReset)
	fmt.Println(colorCyan + bar + colorReset)
}

// Elapsed reports how long a phase took.
//
// Added because "that takes a long time" was the only thing anyone could say
// about a run: the banners say what is happening and nothing says for how
// long, so there is no way to tell a phase that is slow from one that is
// stuck without watching it.
func Elapsed(d time.Duration) {
	fmt.Printf("  %s(%s)%s\n", colorGray, d.Round(time.Second), colorReset)
}

func Info(msg string) { fmt.Printf("  -> %s\n", msg) }
func Ok(msg string)   { fmt.Printf("  %s[ok]%s %s\n", colorGreen, colorReset, msg) }
func Warn(msg string) { fmt.Printf("  %s[!!]%s %s\n", colorYellow, colorReset, msg) }
func Fail(msg string) { fmt.Printf("  %s[FAIL]%s %s\n", colorRed, colorReset, msg) }
