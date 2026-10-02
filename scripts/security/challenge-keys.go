package main

import (
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

// challenge-keys asks about a key handed to something that did not hold it.
//
// WHY THIS EXISTS. Every workflow is already a sensitive path, and a change to
// one already opens a conversation - with one reason for the whole directory.
// So a step gaining a token reads the same as a comment being reworded, and
// that is how the plan step came to hold the job's token while running a pull
// request's own code (#612): the change was reviewed, and nothing in the
// review said "this hands out a credential".
//
// What it does. It reads every workflow as it is at the base of a pull
// request and as it is at the head, works out for each what every job and
// step holds, and lists what the head holds that the base did not:
//
//   - a permission the job's token gains, or gains more of
//   - a secret or a token that reaches a step, a job or a whole workflow
//   - an environment a job runs in, which is what releases that
//     environment's secrets to it
//
// Narrowing is not listed, and neither is a key that was already held. A job
// or a step that is new holds all of its keys newly, and every one is listed.
//
// IT ASKS; IT DOES NOT REFUSE. Handing out a key is often right. The answer
// is a person's, so this writes the list for a review conversation and exits
// clean, and the conversation is what holds the merge until somebody has read
// it. The one thing it refuses is not knowing: a workflow it cannot read is an
// error, because "nothing was widened" and "I could not tell" are different
// answers.
//
// What it cannot see, said in what it writes: what a secret can do once it is
// held - the scope of a vault token or of an App's installation is not in this
// repository - and a token an action takes without being handed one, as a
// checkout takes the job's. For the second it reports the permission that
// makes it possible.
//
// Derived from the two commits and not from a list of who may hold what. Such
// a list would restate what the workflows already say, and the question here
// is about a change, which git already holds both sides of.

// held is what each holder has: holder, then key, then how much of it. A
// permission's amount is its level; every other key is held or it is not.
type held map[string]map[string]string

func (h held) give(holder, key, amount string) {
	if h[holder] == nil {
		h[holder] = map[string]string{}
	}
	// The same key twice is the larger of the two.
	if rank(amount) >= rank(h[holder][key]) {
		h[holder][key] = amount
	}
}

// The keys that are not a secret's name.
const (
	// everyPermission is `read-all` or `write-all`: one level for every scope.
	everyPermission = "permissions (every scope)"
	// defaultPermissions is a job with no permissions stated on it or on its
	// workflow, so its token takes whatever the repository's setting gives.
	defaultPermissions = "permissions left to the repository's default"
	permissionPrefix   = "permission "
	environmentPrefix  = "environment "
	jobToken           = "the job's token"
	inherited          = "every secret the caller holds (secrets: inherit)"
)

func rank(level string) int {
	switch level {
	case "read":
		return 1
	case "write":
		return 2
	case "":
		return 0
	}
	// Held, with no level to it.
	return 1
}

var (
	jobsLine  = regexp.MustCompile(`^jobs:\s*$`)
	stepsLine = regexp.MustCompile(`^    steps:\s*$`)
	stepStart = regexp.MustCompile(`^      - `)
	stepLabel = regexp.MustCompile(`^      (?:- |  )(name|id|uses):\s*(.+?)\s*$`)
	permsLine = regexp.MustCompile(`^( *)permissions:\s*(.*?)\s*$`)
	scopeLine = regexp.MustCompile(`^ +([a-z-]+):\s*(read|write|none)\s*$`)
	envLine   = regexp.MustCompile(`^    environment:\s*(.*?)\s*$`)
	envName   = regexp.MustCompile(`^      name:\s*(.+?)\s*$`)
	secretRef = regexp.MustCompile(`secrets\.([A-Za-z_][A-Za-z0-9_]*)`)
	tokenRef  = regexp.MustCompile(`github\.token`)
	// A token one step mints and another is handed: an App's installation
	// token arrives this way and is in no secret by that name.
	mintedRef  = regexp.MustCompile(`(?i)steps\.([A-Za-z0-9_-]+)\.outputs\.([A-Za-z0-9_-]*token[A-Za-z0-9_-]*)`)
	inheritRef = regexp.MustCompile(`^\s+secrets:\s*inherit\s*$`)
	quoted     = regexp.MustCompile(`^["'](.*)["']$`)
)

// keysIn is every secret and token the text names.
func keysIn(lines []string) []string {
	var out []string
	for _, l := range lines {
		for _, m := range secretRef.FindAllStringSubmatch(l, -1) {
			if m[1] == "GITHUB_TOKEN" {
				out = append(out, jobToken)
				continue
			}
			out = append(out, "secrets."+m[1])
		}
		if tokenRef.MatchString(l) {
			out = append(out, jobToken)
		}
		for _, m := range mintedRef.FindAllStringSubmatch(l, -1) {
			out = append(out, "the token step "+m[1]+" minted ("+m[2]+")")
		}
		if inheritRef.MatchString(l) {
			out = append(out, inherited)
		}
	}
	return out
}

// uncommented drops the lines that are only a comment. A secret named in one
// is prose, and prose hands nothing out.
func uncommented(body string) []string {
	var out []string
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		out = append(out, strings.TrimRight(l, " \r"))
	}
	return out
}

func indent(l string) int { return len(l) - len(strings.TrimLeft(l, " ")) }

// permissions reads the permissions stated at one indent among these lines:
// the scopes and their levels, and whether any were stated at all.
func permissions(lines []string, at int) (scopes map[string]string, stated bool, err error) {
	for i, l := range lines {
		m := permsLine.FindStringSubmatch(l)
		if m == nil || len(m[1]) != at {
			continue
		}
		scopes = map[string]string{}
		switch m[2] {
		case "read-all":
			scopes[everyPermission] = "read"
		case "write-all":
			scopes[everyPermission] = "write"
		case "{}":
		case "":
			for _, s := range lines[i+1:] {
				if strings.TrimSpace(s) == "" {
					continue
				}
				if indent(s) <= at {
					break
				}
				p := scopeLine.FindStringSubmatch(s)
				if p == nil {
					return nil, false, fmt.Errorf("a permissions block holds %q, which is not a scope and its level", strings.TrimSpace(s))
				}
				if p[2] != "none" {
					scopes[permissionPrefix+p[1]] = p[2]
				}
			}
		default:
			return nil, false, fmt.Errorf("permissions are written as %q, which is not read-all, write-all, {} or a block of scopes", m[2])
		}
		return scopes, true, nil
	}
	return nil, false, nil
}

// HeldBy is what every job and step of one workflow holds.
//
// The workflow is read by its indentation, which the formatter fixes: jobs two
// spaces in, a job's own keys four, its steps six. Anything this cannot place
// is an error and never a workflow that holds nothing.
func HeldBy(name, body string) (held, error) {
	if strings.Contains(body, "\t") {
		return nil, fmt.Errorf("%s is indented with a tab, so its jobs and steps cannot be told apart", name)
	}
	lines := uncommented(body)
	var top []string
	jobs := map[string][]string{}
	var order []string
	inJobs, job := false, ""
	for _, l := range lines {
		switch {
		case jobsLine.MatchString(l):
			inJobs, job = true, ""
		case topLevelKey.MatchString(l):
			inJobs, job = false, ""
			top = append(top, l)
		case !inJobs:
			top = append(top, l)
		case jobKey.MatchString(l):
			job = jobKey.FindStringSubmatch(l)[1]
			order = append(order, job)
		case job != "":
			jobs[job] = append(jobs[job], l)
		case strings.TrimSpace(l) != "":
			return nil, fmt.Errorf("%s has %q under jobs and in no job", name, strings.TrimSpace(l))
		}
	}
	if len(order) == 0 {
		return nil, fmt.Errorf("%s has no job this can read, so what it hands out is unknown", name)
	}

	h := held{}
	wfPerms, wfStated, err := permissions(top, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	// Every holder is named even when it holds nothing, so that one which
	// exists is told apart from one that is new.
	h[name+", every job"] = map[string]string{}
	for _, k := range keysIn(top) {
		h.give(name+", every job", k, "held")
	}

	for _, j := range order {
		holder := name + ", job `" + j + "`"
		var own []string
		var steps [][]string
		inSteps := false
		for _, l := range jobs[j] {
			switch {
			case stepsLine.MatchString(l):
				inSteps = true
			case inSteps && stepStart.MatchString(l):
				steps = append(steps, []string{l})
			case inSteps && strings.TrimSpace(l) != "" && indent(l) <= 4:
				inSteps = false
				own = append(own, l)
			case inSteps && len(steps) > 0:
				steps[len(steps)-1] = append(steps[len(steps)-1], l)
			case inSteps && strings.TrimSpace(l) != "":
				return nil, fmt.Errorf("%s: job %s has %q under steps and in no step", name, j, strings.TrimSpace(l))
			default:
				own = append(own, l)
			}
		}
		calls := false
		for _, l := range own {
			if strings.HasPrefix(l, "    uses:") {
				calls = true
			}
		}
		if len(steps) == 0 && !calls {
			return nil, fmt.Errorf("%s: job %s has no step this can read and calls no workflow, so what it holds is unknown", name, j)
		}

		perms, stated, err := permissions(own, 4)
		if err != nil {
			return nil, fmt.Errorf("%s: job %s: %w", name, j, err)
		}
		switch {
		case stated:
		case wfStated:
			perms = wfPerms
		default:
			perms = map[string]string{defaultPermissions: "held"}
		}
		h[holder] = map[string]string{}
		h[holder+", every step"] = map[string]string{}
		for scope, level := range perms {
			h.give(holder, scope, level)
		}
		for i, l := range own {
			m := envLine.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			env := m[1]
			if env == "" && i+1 < len(own) {
				if n := envName.FindStringSubmatch(own[i+1]); n != nil {
					env = n[1]
				}
			}
			if q := quoted.FindStringSubmatch(env); q != nil {
				env = q[1]
			}
			if env == "" {
				return nil, fmt.Errorf("%s: job %s runs in an environment this cannot read the name of", name, j)
			}
			h.give(holder, environmentPrefix+env, "held")
		}
		for _, k := range keysIn(own) {
			h.give(holder+", every step", k, "held")
		}

		seen := map[string]int{}
		for i, step := range steps {
			label := fmt.Sprintf("step %d", i+1)
			found := map[string]string{}
			for _, l := range step {
				if m := stepLabel.FindStringSubmatch(l); m != nil && found[m[1]] == "" {
					found[m[1]] = m[2]
				}
			}
			for _, key := range []string{"name", "id", "uses"} {
				if found[key] != "" {
					label = found[key]
					if q := quoted.FindStringSubmatch(label); q != nil {
						label = q[1]
					}
					break
				}
			}
			seen[label]++
			if seen[label] > 1 {
				label = fmt.Sprintf("%s (%d)", label, seen[label])
			}
			h[holder+", step `"+label+"`"] = map[string]string{}
			for _, k := range keysIn(step) {
				h.give(holder+", step `"+label+"`", k, "held")
			}
		}
	}
	return h, nil
}

// Widened is what the head holds that the base did not, one line each, in
// order. A permission is widened when its level rises; anything else when it
// is held and was not.
func Widened(base, head held) []string {
	var out []string
	for holder, keys := range head {
		before, existed := base[holder]
		for key, amount := range keys {
			was := before[key]
			if strings.HasPrefix(key, permissionPrefix) && rank(before[everyPermission]) > rank(was) {
				was = before[everyPermission]
			}
			if rank(amount) <= rank(was) {
				continue
			}
			line := holder + ": "
			switch {
			case strings.HasPrefix(key, permissionPrefix):
				line += "`" + strings.TrimPrefix(key, permissionPrefix) + ": " + amount + "`"
			case key == everyPermission:
				line += "`" + amount + "-all`"
			case strings.HasPrefix(key, environmentPrefix):
				line += "runs in the environment `" + strings.TrimPrefix(key, environmentPrefix) + "`, and so can be handed its secrets"
			case key == defaultPermissions:
				line += "states no permissions, so its token takes the repository's default"
			default:
				line += "holds " + key
			}
			switch {
			case !existed:
				line += " (it is new)"
			case was != "" && was != "held":
				line += " (was " + was + ")"
			}
			out = append(out, line)
		}
	}
	sort.Strings(out)
	return out
}

// workflowFile is what counts as a workflow: a YAML file directly in the
// workflows directory. The actions beside them are called from a step, and
// what a step hands one is read at the step.
var workflowFile = regexp.MustCompile(`^\.github/workflows/([^/]+\.ya?ml)$`)

// Git reads the repository: a parameter so a test can say what it holds.
type Git func(args ...string) ([]byte, error)

func execGit(args ...string) ([]byte, error) {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	cmd := exec.Command("git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// heldAt is what every workflow holds at one commit.
func heldAt(git Git, commit string) (held, map[string]string, error) {
	// From the top of the repository, wherever this was started.
	listed, err := git("ls-tree", "-r", "--full-tree", "--name-only", commit, "--", ".github/workflows")
	if err != nil {
		return nil, nil, err
	}
	all, files := held{}, map[string]string{}
	for _, path := range strings.Split(strings.TrimSpace(string(listed)), "\n") {
		m := workflowFile.FindStringSubmatch(path)
		if m == nil {
			continue
		}
		body, err := git("show", commit+":"+path)
		if err != nil {
			return nil, nil, err
		}
		h, err := HeldBy(m[1], string(body))
		if err != nil {
			return nil, nil, err
		}
		for holder, keys := range h {
			all[holder] = keys
			files[holder] = path
		}
	}
	// A repository with no workflow at all is not what this was pointed at.
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("%s holds no workflow this can find, so what it hands out is unknown", commit)
	}
	return all, files, nil
}

// Challenge is what a pull request widened, as a conversation: the report to
// post, a digest of exactly what was widened, and the file to hang it on.
// Nothing widened is no report.
type Challenge struct {
	Widened []string
	Report  string
	Digest  string
	Anchor  string
}

func challenge(git Git, base, head string) (Challenge, error) {
	before, _, err := heldAt(git, base)
	if err != nil {
		return Challenge{}, fmt.Errorf("reading the workflows at the base: %w", err)
	}
	after, files, err := heldAt(git, head)
	if err != nil {
		return Challenge{}, fmt.Errorf("reading the workflows at the head: %w", err)
	}
	c := Challenge{Widened: Widened(before, after)}
	if len(c.Widened) == 0 {
		return c, nil
	}
	// The digest is of what was widened and nothing else, so a later push
	// that widens nothing further leaves an answer already given standing,
	// and one that hands out anything more asks again.
	c.Digest = fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(c.Widened, "\n"))))[:12]
	// Hung on a workflow this pull request changed: the first, in order, that
	// holds something it did not.
	var anchors []string
	for holder, keys := range after {
		if len(Widened(before, held{holder: keys})) > 0 {
			anchors = append(anchors, files[holder])
		}
	}
	sort.Strings(anchors)
	c.Anchor = anchors[0]

	var b strings.Builder
	b.WriteString("### This hands out a key that was not held before\n\n")
	b.WriteString("Each line is something a job or a step holds after this pull request and did not before it. ")
	b.WriteString("If every line is meant, resolve this conversation. If one is not, that is the change to take back.\n\n")
	for _, w := range c.Widened {
		b.WriteString("- " + w + "\n")
	}
	b.WriteString("\nNot seen from here: what a secret can do once it is held, and a token an action takes without being handed one - for that, read the permissions above.\n")
	c.Report = b.String()
	return c, nil
}

// reportDelimiter closes the report in the output a workflow reads. A line no
// report can hold: every line of one starts with a heading, a dash or prose.
const reportDelimiter = "__THE_KEYS_CHALLENGED__"

func challengeKeys(args []string) int {
	return challengeKeysTo(args, execGit, os.Stdout, os.Stderr)
}

func challengeKeysTo(args []string, git Git, out, errs io.Writer) int {
	fs := flag.NewFlagSet("challenge-keys", flag.ContinueOnError)
	fs.SetOutput(errs)
	base := fs.String("base", "", "the commit the pull request is based on")
	head := fs.String("head", "", "the commit the pull request would merge")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *base == "" || *head == "" {
		fmt.Fprintln(errs, "security challenge-keys: need -base and -head, the two commits to compare")
		return 2
	}
	c, err := challenge(git, *base, *head)
	if err != nil {
		// Not knowing is not the same as nothing having been handed out.
		fmt.Fprintln(errs, "security challenge-keys: "+err.Error())
		return 1
	}
	if len(c.Widened) == 0 {
		fmt.Fprintln(out, "asked=false")
		fmt.Fprintln(errs, "security challenge-keys: no job or step holds a key it did not hold before")
		return 0
	}
	fmt.Fprintln(out, "asked=true")
	fmt.Fprintln(out, "digest="+c.Digest)
	fmt.Fprintln(out, "anchor="+c.Anchor)
	fmt.Fprintln(out, "report<<"+reportDelimiter)
	fmt.Fprint(out, c.Report)
	fmt.Fprintln(out, reportDelimiter)
	fmt.Fprintf(errs, "security challenge-keys: %d key(s) handed out that were not held before; a conversation asks whether each is meant\n", len(c.Widened))
	for _, w := range c.Widened {
		fmt.Fprintln(errs, "  "+w)
	}
	return 0
}
