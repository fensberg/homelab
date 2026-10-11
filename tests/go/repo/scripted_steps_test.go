package repo

import (
	"sort"
	"strings"
	"testing"
)

// A step OpenTofu runs as a script is run by /bin/sh unless it names
// something else, and which sh that is depends on the machine.
//
// The step that applies the network manifest is bash: its first line sets
// `pipefail`. It named no interpreter, so it worked from a workstation whose
// sh takes that option, and on the estate's own runner it stopped within
// milliseconds of starting, before it had asked the cluster anything. A
// change to the network could then be delivered by hand and never by a
// merge, and nothing said so until a merge tried (2026-10-11).
func TestEveryScriptedStepThatIsBashSaysSo(t *testing.T) {
	const step, terminator = `provisioner "local-exec"`, "EOT"

	var wrong []string
	seen := 0
	for path, body := range tofuSources(t) {
		rest := body
		for {
			at := strings.Index(rest, step)
			if at < 0 {
				break
			}
			rest = rest[at+len(step):]
			// The step runs to the line that ends its script.
			block := rest
			if end := strings.Index(rest, "\n"+indentOf(rest, terminator)+terminator+"\n"); end >= 0 {
				block = rest[:end]
			}
			seen++
			if strings.Contains(block, "pipefail") && !strings.Contains(block, `interpreter = ["/bin/bash", "-c"]`) {
				wrong = append(wrong, path)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no scripted step was found in any OpenTofu file, so this checked nothing. If they were all removed, remove this with them.")
	}
	sort.Strings(wrong)
	if len(wrong) > 0 {
		t.Fatalf(`%d scripted step(s) set pipefail and do not name bash as what runs them:

  %s

Left to the default the script runs under the machine's /bin/sh, and not
every sh has pipefail: such a step runs from one machine and stops at its
first line on another. Give the step
  interpreter = ["/bin/bash", "-c"]`, len(wrong), strings.Join(wrong, "\n  "))
	}
}

// indentOf is the spaces in front of the first line of s that is word and
// nothing else.
func indentOf(s, word string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == word {
			return line[:len(line)-len(strings.TrimLeft(line, " "))]
		}
	}
	return ""
}
