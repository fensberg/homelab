package console

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// Output that is not going to a terminal carries no colour codes. They once
// went out unconditionally and reached a pull request comment as literal
// "^[[32m[ok]^[[0m", which nobody could read.
func TestNothingPrintedToAPipeCarriesAnEscapeCode(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	useColor = isTerminal(w) && os.Getenv("NO_COLOR") == ""
	Phase("render", "a description")
	Elapsed(3 * time.Second)
	Info("info")
	Ok("ok")
	Warn("warn")
	Fail("fail")
	os.Stdout = stdout
	w.Close()
	out, _ := io.ReadAll(r)

	if strings.Contains(string(out), "\033") {
		t.Errorf("output to a pipe carries escape codes:\n%q", out)
	}
	for _, want := range []string{"RENDER", "(3s)", "-> info", "[ok] ok", "[!!] warn", "[FAIL] fail"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}
