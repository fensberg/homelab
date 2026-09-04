// Command inspector reads a change and says what a person would want to know
// about it before signing it off.
//
// The inspector is the party who checks work before it may be covered up. It
// has two halves and they fail differently, so they are separate verbs:
//
//	tally    what this change takes away - git only, no model, no credential
//
// tally holds nothing and calls nobody. It cannot fail because a vendor is
// unreachable, a key expired or a quota ran out, which is what makes it the
// half worth having in front of a merge. The half that needs a model to
// explain a sensitive-path change is a separate verb, and it degrades to the
// static reason when the model does not answer - it never becomes a
// precondition of merging.
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "tally" {
		fmt.Fprint(os.Stderr, "inspector reads a change before it is covered up.\n\nusage: inspector tally -base <sha> -head <sha> [-root <dir>]\n")
		os.Exit(2)
	}
	os.Exit(tally(os.Args[2:]))
}

func tally(args []string) int {
	var base, head, root string
	root = "."
	for i := 0; i+1 < len(args); i += 2 {
		switch args[i] {
		case "-base":
			base = args[i+1]
		case "-head":
			head = args[i+1]
		case "-root":
			root = args[i+1]
		}
	}
	if base == "" || head == "" {
		fmt.Fprintln(os.Stderr, "inspector tally: -base and -head are both required")
		return 2
	}

	r, err := take(root, base, head)
	if err != nil {
		// Could-not-compare is not nothing-was-removed. The same rule the
		// Health phase learned the expensive way: a check that cannot answer
		// must say so rather than return the reassuring verdict.
		fmt.Fprintf(os.Stderr, "inspector tally: could not compare %s..%s: %v\n", base, head, err)
		fmt.Println("## What this change takes away\n\n**Could not be determined.** " +
			"The two commits could not be compared, so nothing here says anything about the change.")
		return 1
	}

	fmt.Print(render(r))
	// Always zero when it ran. This reports; it does not refuse. A removal is
	// usually correct, and a check that is usually wrong gets ignored and then
	// gets deleted.
	return 0
}

func render(r *report) string {
	var b strings.Builder
	b.WriteString("## What this change takes away\n\n")

	// Only a NEGATIVE delta is a removal. Adding tests raises the count, and a
	// report that says something went when something arrived is a report
	// people stop reading.
	var lost []string
	for pkg, delta := range r.assertions {
		if delta < 0 {
			lost = append(lost, fmt.Sprintf("- `%s` — %d fewer", pkg, -delta))
		}
	}

	if len(r.files) == 0 && len(r.tests) == 0 && len(lost) == 0 && len(r.newTools) == 0 {
		b.WriteString("Nothing. No files removed under `tests/`, `.github/` or `scripts/`, " +
			"no test functions removed, and no package lost assertions.\n")
		return b.String()
	}

	if len(r.tests) > 0 {
		names := make([]string, 0, len(r.tests))
		for n := range r.tests {
			names = append(names, n)
		}
		sort.Strings(names)

		fmt.Fprintf(&b, "### %d test function(s) removed\n\n", len(names))
		for _, n := range names {
			fmt.Fprintf(&b, "- `%s` — was in `%s`\n", n, r.tests[n])
		}
		b.WriteString("\n")
	}

	if len(r.files) > 0 {
		fmt.Fprintf(&b, "### %d file(s) removed under a watched path\n\n", len(r.files))
		for _, f := range r.files {
			fmt.Fprintf(&b, "- `%s`\n", f)
		}
		b.WriteString("\n")
	}

	if len(lost) > 0 {
		sort.Strings(lost)
		b.WriteString("### Packages with fewer assertions\n\n")
		b.WriteString(strings.Join(lost, "\n"))
		b.WriteString("\n\nA test that keeps its name and loses its assertions still passes, " +
			"and still reports coverage that is no longer there.\n\n")
	}

	if len(r.newTools) > 0 {
		fmt.Fprintf(&b, "### %d new binary invocation(s) the runner image may not carry\n\n", len(r.newTools))
		for _, t := range r.newTools {
			fmt.Fprintf(&b, "- `%s` — invoked by this change, and `.github/runner-image/Dockerfile` does not mention it\n", t)
		}
		b.WriteString("\nA tool the code needs and the image lacks fails minutes into a converge, " +
			"on the runner, with a message naming the subsystem it never reached rather than the tool. " +
			"This is read off the change, so the thing introducing the dependency is the thing that raises it.\n\n")
	}

	b.WriteString("---\n\nThis is a statement, not an objection. " +
		"Removals and new dependencies are usually deliberate; this exists so a " +
		"deliberate one is visible rather than inferred from the size of the diff. " +
		"A binary named by a variable cannot be read off the source, so the tool list " +
		"is what was found rather than everything there is.\n")
	return b.String()
}
