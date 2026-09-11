package repo

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every job's outbound reach is what scripts/approved-suppliers.yml says.
//
// The rule lives in `security guard-egress` rather than here, because it is a
// refusal rather than an observation: it runs from `task validate` and the
// pre-push hook, where it stops work before it is published. This runs the same
// verb so CI answers the question too, and so the mutation ledger can prove it
// catches something - the ledger judges tests in this package.
//
// It is deliberately the verb and not a reimplementation. Two readers of one
// rule drift, and the copy that runs in CI would be the one nobody notices is
// wrong.
func TestEveryJobsEgressIsDeclaredInTheSuppliersList(t *testing.T) {
	root := repoRoot(t)

	// -root, because the ledger runs this against a scratch copy of the
	// repository that has no .git for the verb's walk upwards to find.
	cmd := exec.Command("go", "run", "-C", filepath.Join(root, "scripts", "security"), ".",
		"guard-egress", "-root", root)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err == nil {
		return
	}

	body := string(out)
	if strings.Contains(body, "no jobs were found") || strings.Contains(body, "declares no egress at all") {
		t.Fatalf("guard-egress could not see the repository, so nothing was checked:\n%s", body)
	}
	t.Errorf(`a job's outbound reach is not what scripts/approved-suppliers.yml declares:

%s`, body)
}
