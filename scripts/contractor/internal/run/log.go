package run

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Colour is written only to a terminal.
//
// These codes used to go out unconditionally, so a converge's output reached a
// pull request comment as literal escape sequences - "^[[32m[ok]^[[0m" - which
// is unreadable and made a comment nobody could act on. A pipe is not a
// terminal and never wants them.
//
// NO_COLOR is honoured because it is the convention, and because it gives
// anyone debugging a captured log a way to reproduce what CI sees.
var useColor = isTerminal(os.Stdout) && os.Getenv("NO_COLOR") == ""

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// paint wraps s in a colour, or returns it untouched when nothing will render
// the escape sequences.
func paint(color, s string) string {
	if !useColor {
		return s
	}
	return color + s + colorReset
}

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
	fmt.Println(paint(colorCyan, bar))
	fmt.Println(paint(colorCyan, fmt.Sprintf(" PHASE %d : %s", phaseNum, strings.ToUpper(name))))
	fmt.Println(paint(colorGray, " "+description))
	fmt.Println(colorCyan + bar + colorReset)
}

// Elapsed reports how long a phase took.
//
// Added because "that takes a long time" was the only thing anyone could say
// about a run: the banners say what is happening and nothing says for how
// long, so there is no way to tell a phase that is slow from one that is
// stuck without watching it.
func Elapsed(d time.Duration) {
	fmt.Printf("  %s\n", paint(colorGray, fmt.Sprintf("(%s)", d.Round(time.Second))))
}

func Info(msg string) { fmt.Printf("  -> %s\n", msg) }
func Ok(msg string)   { fmt.Printf("  %s %s\n", paint(colorGreen, "[ok]"), msg) }
func Warn(msg string) { fmt.Printf("  %s %s\n", paint(colorYellow, "[!!]"), msg) }
func Fail(msg string) { fmt.Printf("  %s %s\n", paint(colorRed, "[FAIL]"), msg) }
