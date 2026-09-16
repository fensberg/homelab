package repo

import (
	"regexp"
	"strings"
	"testing"
)

// A hostname pasted out of a terminal is how names actually leak.
//
// WHAT HAPPENED. A real hypervisor name sat in
// management/hypervisor/hypervisor-prep.yml, inside a comment recording a
// terminal session (#343):
//
//	#     # pvesh set /cluster/sdn
//	#     <hostname>: reloading network config
//
// forkable_test.go was green the whole time, and correctly so. It works on
// SHAPES the estate generates - `<site>-cp-NN`, a resource address key - and
// catches a real name only when it appears in one. A bare hostname in prose is
// just a word: there is nothing to match on, and no list of real names can be
// committed to compare against, because those names are the thing being kept
// out.
//
// WHY THIS IS THE CLASS WORTH CLOSING. CLAUDE.md already names the cause:
// "The recurring way this breaks is pasting terminal transcripts. Recording
// evidence verbatim is the right instinct and is exactly the operation that
// carries real names and addresses into a public repository without anybody
// deciding to." So the one class of leak the record predicts was the one class
// the guard could not see.
//
// WHAT THIS CAN AND CANNOT CATCH, said plainly because a guard whose edges
// nobody knows is worse than none. Transcripts have shapes of their own even
// when hostnames do not - a shell prompt, an ssh invocation, per-host tool
// output - and those are what this matches. A hostname written into ordinary
// prose ("I ran this on the big one") stays undetectable by construction, and
// the answer there is review.
//
// RESTRICTED TO COMMENTS AND PROSE, deliberately. Code legitimately builds
// these strings from configuration - `"root@" + h` in the SSH preflight, or a
// format string with a %s in it - and matching there would refuse the program
// doing its job. A transcript is by definition something somebody pasted, and
// what somebody pastes lands in a comment or a document.

// The shapes a pasted terminal session takes. Each captures the host part.
var transcriptShapes = []struct {
	name string
	re   *regexp.Regexp
}{
	{
		// A shell prompt: `root@host:~#`, `user@host:/path$`.
		name: "a shell prompt",
		re:   regexp.MustCompile(`\b[a-z_][a-z0-9_-]*@([a-z][a-z0-9.-]*[a-z0-9])\s*:\s*[~/]`),
	},
	{
		// An ssh invocation: `ssh root@host`, `ssh-copy-id root@host`.
		name: "an ssh invocation",
		re:   regexp.MustCompile(`\bssh(?:-copy-id)?\s+(?:-\S+\s+)*[a-z_][a-z0-9_-]*@([a-z][a-z0-9.-]*[a-z0-9])\b`),
	},
	{
		// Ansible's own per-host output: `ok: [host]`, `changed: [host]`.
		name: "per-host tool output",
		re:   regexp.MustCompile(`^(?:ok|changed|skipping|failed|fatal|unreachable)\s*:\s*\[([a-z][a-z0-9.-]*[a-z0-9])\]`),
	},
	{
		// Proxmox's, which carries no prefix at all: `host: message`. This is
		// the shape that actually leaked (#343), and it is the hardest one -
		// `word: text` at the start of a comment line is also how half the
		// prose in this repository is written.
		//
		// So the host part has to LOOK like a host rather than like a word:
		// either `name-NN`, which is what Proxmox and most fleets number their
		// machines with, or a dotted name whose last label is alphabetic. That
		// admits `pve-01:` and `hv.internal:` and refuses `Note:`, `Fix:`,
		// `pre-commit:` and `v1.7.12:`.
		//
		// It will not catch a single bare word that happens to be a real
		// hostname. Nothing can: that is the limit named in the header, and it
		// is review's job.
		name: "per-host output with no prefix",
		re:   regexp.MustCompile(`^(?:\$\s+)?([a-z][a-z0-9]*-\d+|[a-z][a-z0-9-]+(?:\.[a-z0-9-]+)*\.[a-z]{2,})\s*:\s+\S`),
	},
}

// Host parts that carry no information about a real place.
//
// Anything a documented placeholder site would produce, plus the handful of
// names that are generic by construction - a loopback, a service name inside a
// cluster, a vendor's own documentation host.
var placeholderHosts = map[string]bool{
	"localhost": true, "host": true, "hostname": true, "example.com": true,
	"example.org": true, "example.net": true, "invalid": true,
	"server": true, "node": true, "remote": true, "target": true,
	"user": true, "pve": true, "proxmox": true, "hypervisor": true,
}

// Extensions that make a dotted name a FILE rather than a host.
//
// `variables.tf: local.state_db_nodeport ...` is a comment naming a file and
// what is in it, and it has exactly the shape of Proxmox per-host output. Both
// instances in this repository were that, which is a good sign about the
// pattern and a necessary exclusion.
var fileExtensions = map[string]bool{
	"tf": true, "tfvars": true, "hcl": true, "go": true, "mod": true, "sum": true,
	"yml": true, "yaml": true, "json": true, "toml": true, "env": true, "cfg": true,
	"conf": true, "sh": true, "bash": true, "md": true, "markdown": true, "txt": true,
	"ts": true, "js": true, "mts": true, "patch": true, "lock": true, "tpl": true,
	"service": true, "sarif": true, "out": true, "log": true, "xz": true, "gz": true,
}

func looksLikeAFilename(name string) bool {
	i := strings.LastIndex(name, ".")
	return i > 0 && fileExtensions[name[i+1:]]
}

func isPlaceholderHost(host string) bool {
	host = strings.ToLower(host)
	if looksLikeAFilename(host) {
		return true
	}
	if placeholderHosts[host] {
		return true
	}
	// A dotted name is a placeholder when its first label is, so
	// `hypervisor.example.com` and `site0-cp-100.internal` both pass.
	first := host
	if i := strings.Index(host, "."); i > 0 {
		first = host[:i]
		if strings.HasSuffix(host, ".example.com") || strings.HasSuffix(host, ".example.org") ||
			strings.HasSuffix(host, ".example.net") || strings.HasSuffix(host, ".invalid") {
			return true
		}
	}
	if placeholderHosts[first] || isPlaceholderSite(first) {
		return true
	}
	// `<site>-cp-100`, `<site>-worker-200`: the VM naming scheme, whose site
	// part forkable_test.go already governs.
	if m := regexp.MustCompile(`^([a-z][a-z0-9-]*?)-(?:cp|worker)-\d+$`).FindStringSubmatch(first); m != nil {
		return isPlaceholderSite(m[1])
	}
	// An angle-bracket or brace placeholder - `<host>`, `${host}` - has
	// already been stripped of its brackets by the patterns above only when it
	// is bare, so the literal forms are checked here.
	return strings.ContainsAny(host, "<>{}$") || first == ""
}

// commentaryOf returns only the lines of a file that are commentary or prose,
// so code that legitimately builds these strings is not read as a transcript.
func commentaryOf(rel, body string) []string {
	var out []string
	markdown := strings.HasSuffix(rel, ".md") || strings.HasSuffix(rel, ".markdown") ||
		strings.HasSuffix(rel, ".txt")

	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case markdown:
			out = append(out, trimmed)
		case strings.HasPrefix(trimmed, "#"), strings.HasPrefix(trimmed, "//"),
			strings.HasPrefix(trimmed, "*"), strings.HasPrefix(trimmed, "--"):
			// Whole-line comments in every language this repository writes.
			out = append(out, strings.TrimLeft(trimmed, "#/*- \t"))
		}
	}
	return out
}

func TestNoTerminalTranscriptCarriesARealHostname(t *testing.T) {
	checked := 0
	walkText(t, func(rel, body string) {
		if strings.HasSuffix(rel, "transcripts_test.go") {
			return // this file carries the shapes in order to describe them
		}
		checked++

		for _, line := range commentaryOf(rel, body) {
			for _, shape := range transcriptShapes {
				for _, m := range shape.re.FindAllStringSubmatch(line, -1) {
					if isPlaceholderHost(m[1]) {
						continue
					}
					t.Errorf(`%s carries %s naming the host %q:

    %s

This repository is public and forkable, and a site's hostnames live in the
vault precisely so they are not in it. Pasting a terminal session verbatim is
the right instinct for recording evidence and is exactly the operation that
carries a real name in without anybody deciding to - which is why this shape
gets a check rather than a reminder.

Replace the host with a documented placeholder: example, north-street-office,
redacted, or a positional site0 / site10.`, rel, shape.name, m[1], line)
				}
			}
		}
	})

	const atLeastAHundredTextFiles = 100
	if checked < atLeastAHundredTextFiles {
		t.Fatalf(`only %d text file(s) were read, and this repository has far more.

The walk has stopped matching, so every file it no longer sees is one this
check silently stopped reading - which is the same green as finding nothing.`, checked)
	}
}

// The shapes match what they are meant to, and the placeholders pass.
//
// A table because the failure mode of a pattern that stops matching is silence:
// it reports nothing, looks clean, and is indistinguishable from a repository
// with no transcripts in it. That is the exact shape this whole file exists to
// refuse, so the patterns are held to it too.
func TestTheTranscriptShapesMatchWhatTheyClaim(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		wantHit bool
	}{
		{"the comment that leaked", "pve-01: reloading network config", true},
		{"ansible per-host output", "changed: [pve-01]", true},
		{"ansible output, placeholder host", "changed: [north-street-office]", false},
		{"a root prompt", "root@pve-01:~# pvesh set /cluster/sdn", true},
		{"a root prompt, placeholder host", "root@redacted:~# pvesh set /cluster/sdn", false},
		{"a user prompt", "dev@devbox:~/homelab$ task test", true},
		{"an ssh invocation", "ssh root@pve-01", true},
		{"ssh to a placeholder", "ssh root@example", false},
		{"ssh-copy-id, real host", "ssh-copy-id root@pve-01", true},
		{"an email address is not a prompt", "write to someone@example.com about it", false},
		{"a bracketed placeholder", "ssh root@<hypervisor>", false},
		{"a VM name with a placeholder site", "root@site0-cp-100:~# talosctl version", false},
		{"ordinary prose", "the hypervisor reloaded its network config", false},
		{"prose with a colon", "Note: this is how it works", false},
		{"a hyphenated tool name", "pre-commit: installs seven repositories", false},
		{"a version with dots", "v1.7.12: bumped eleven releases", false},
		{"a placeholder host with a number", "site0-cp-100: reloading", false},
		{"a dotted internal host", "hv.internal.example.com: reloading", false},
		{"a comment naming a file", "variables.tf: local.state_db_nodeport and friends", false},
		{"a comment naming a workflow", "pr-validation.yml: eleven lanes in parallel", false},
		{"a real host with a domain", "hv.internal.corp: reloading network config", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hit := false
			for _, shape := range transcriptShapes {
				for _, m := range shape.re.FindAllStringSubmatch(tc.line, -1) {
					if !isPlaceholderHost(m[1]) {
						hit = true
					}
				}
			}
			if hit != tc.wantHit {
				t.Errorf("matched=%v, want %v, for:\n  %s", hit, tc.wantHit, tc.line)
			}
		})
	}
}
