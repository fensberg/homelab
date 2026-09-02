package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A precondition that nothing calls is a precondition that does not exist.
//
// This is the shape that has escaped a full test run four times this week. The
// list of checks gets tested, the checks themselves get tested, and nothing
// asserts that anything runs them - so deleting the one line that does leaves
// every test green and the guard switched off.
//
// The change detector catches it for unexported functions inside one package.
// These are exported and called across packages, which that check deliberately
// does not follow, because chasing exported symbols everywhere turns a sharp
// check into a noisy one. This is the narrow version: whatever declares itself
// a precondition runner must be called from somewhere that is not its own file
// and is not a test.
func TestEveryPreconditionRunnerIsCalled(t *testing.T) {
	root := filepath.Join(repoRoot(t), "scripts", "contractor")

	declared := map[string]string{} // name -> file that declares it
	callers := map[string]bool{}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range preconditionRunner.FindAllStringSubmatch(string(body), -1) {
			declared[m[1]] = path
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	if len(declared) == 0 {
		t.Fatal("no precondition runners found at all. Either they have been renamed, " +
			"in which case this check now guards nothing, or every guard has been " +
			"deleted - and passing on an empty set is how a check stops mattering.")
	}

	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for name, declaredIn := range declared {
			if path == declaredIn {
				continue
			}
			if strings.Contains(string(body), name+"(") {
				callers[name] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	for name, declaredIn := range declared {
		if !callers[name] {
			rel, _ := filepath.Rel(repoRoot(t), declaredIn)
			t.Errorf("%s declares %s and nothing outside that file calls it.\n\n"+
				"Its list is tested and its checks are tested, so deleting the one line "+
				"that runs it leaves every test green and the guard switched off. That "+
				"has happened four times this week.", rel, name)
		}
	}
}

// `func CheckSomethingPreconditions(` - the runner, as opposed to the list it
// iterates.
var preconditionRunner = regexp.MustCompile(`func (Check\w*Preconditions)\(`)
