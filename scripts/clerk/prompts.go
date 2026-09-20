package main

// What the clerk is asked.
//
// The platform facts in findingRules are each a false positive the clerk
// actually produced, recorded where every prompt reads them rather than argued
// with in a pull request thread. `$/` was reported as invalid syntax on #442,
// in the same run where the step using it had succeeded.
//
// The rule about pairing an account with its own path is the same kind of
// record. On #456 it reported that tests/mutations.yml disagreed with
// commentary describing "a script running against a stub command" - prose from
// a different file - and the finding was therefore about a disagreement it had
// assembled itself.
//
// Never "where do the docs and the code disagree" in one breath. Asked that
// way, a reader sees the claim first and reads the code looking for it, and
// reports agreement it was primed to find. So the reading happens blind and
// the comparison happens afterwards, against an account written by someone who
// had not seen the claim.

const findingRules = `
Answer with JSON and nothing else. A list of findings, each an object:

  {"rule": "<rule>", "path": "<path as printed in the === header ===>", "line": <number>, "message": "<one or two sentences>"}

Rules that apply to every finding:
- The line number is the one printed at the start of the line, in the "N| " prefix.
- A finding you cannot pin to a path and a line does not go in the list. There is no way to report one.
- Say what is wrong and where. Do not suggest a rewrite, do not praise anything, do not describe the file as a whole.
- Report nothing you are not reasonably sure of. An empty list is a fine answer.
- A finding comparing commentary against an account must use the account FOR THAT SAME PATH. If the two you are holding describe different files, there is no disagreement to report - say nothing rather than reporting the mismatch you created by pairing them.

Facts about the platforms in this repository that a reader has previously reported as mistakes. They are not mistakes; do not report them:
- In a GitHub Actions workflow, "uses: $/<path>" is the self-repository action reference. It resolves the action at the commit being run, and it is valid syntax that every workflow here depends on.
`

// blindPrompt reads the work without being told anything about it.
//
// The commentary has been blanked out, which is what makes the structural half
// honest: a comment explaining why a block exists is exactly what stops a
// reader noticing that nothing reaches it.
const blindPrompt = `You are reading source from a repository you have never seen. All comments have been removed. Nobody has told you what any of it is for, and you must not assume there is a good reason for anything.

Do two things.

First, work out what this code actually does, and write it down plainly - ONE ACCOUNT PER FILE, keyed by the path exactly as printed in its "=== path ===" header. This is for your own use in a later step; keep each one short.

Write each account from that file alone. Do not describe the repository, the project, or what the files have in common - an account that could have been written about any file in any project is worse than useless in the step that follows, because every specific claim then looks like an addition to it. If a file leaves you with little to say, say little: "a locals block computing subnet arithmetic from an octet" is a good account. Inventing purpose is not.

Second, list what is not built soundly. Look for things like:
- code nothing reaches, or a branch that cannot be taken
- a part of a file that connects to nothing else in it
- a function that calls itself, or a value written and then read by nobody but the thing that wrote it
- the same work done twice in two places
- a name that says one thing while the thing beside it does another
- an error that is swallowed, or a failure path that cannot be reached
- structure so tangled that you cannot follow what happens when

Before reporting anything about control flow - an unreachable branch, a condition that skips what it should keep, a loop that does not do what its body suggests - work the logic through on one concrete input and satisfy yourself that the input really does reach the wrong place. Boolean conditions with "and", "or" and negation are where this goes wrong most often, and a claim about one that has not been traced is usually the reader's mistake rather than the code's. If you cannot name the input, do not report it.

Use the rule "unsound-work" for every finding.

Answer with JSON and nothing else, as one object:

  {"accounts": {"<path>": "<what that file does, plainly>", ...}, "findings": [ ... ]}
` + findingRules + `
The code follows, with line numbers.
`

// comparePrompt is shown the claim only after the account exists.
//
// The two "never findings" are not general caution; each is a false positive
// this prompt actually produced.
//
// The silence rule was already here as a trailing sentence and was ignored:
// the clerk reported "the commentary mentions X, but the account does not
// mention any such context", which is the silence case restated as a finding
// (#255). A rule buried at the end of a paragraph is a rule a model skips, so
// it is numbered, capitalised, and given the exact sentence to delete.
//
// The second rule names something the prompt never told the model. What
// arrives as "commentary" is whatever strip.go pulled out, and outside Go that
// is a line-prefix match - so a `#` inside a YAML block scalar, a shell
// heredoc, or a sample diff arrives labelled as prose. The model was asked to
// judge data as though it were a claim and did so reasonably. Describing the
// pipeline honestly is cheaper and more general than teaching the stripper
// every language's quoting rules, and #255 still tracks doing both.
//
// "does not support" became "CONTRADICTS" for the same reason: the weaker verb
// invites exactly the silence finding the first rule forbids.
//
// The third rule and the both-halves requirement were added after the first two
// were live and the same shape arrived twice more: on #259 a note that linting
// is owned by Super-Linter in a different file, reported as disagreeing with an
// account of this one; and on #267 a comment explaining what an earlier version
// of a loop did, reported because the account does not describe it. Neither
// said "the account is silent" - rule 1 stopped that wording, and the finding
// reappeared as a manufactured contradiction instead.
//
// That is the lesson worth more than the rules: hardening a prohibition can
// move a failure rather than remove it. Requiring both halves to be quoted
// attacks the manufacture directly, where forbidding one phrasing only renamed
// it.
//
// A fourth distinct shape DID appear, twice - #326 and #359 - and the note
// above is what stopped a fourth rule being written. It was right. Neither was
// reachable by a rule, because in both the model was obeying the three it had:
// it quoted a statement from the account, as rule 1 requires, and the statement
// it quoted was about a different file.
//
// The cause was structural and one level up. `read` concatenated every changed
// file's code into one bundle and every file's commentary into another, so the
// blind pass wrote ONE account of the whole lot. On a change carrying plan.go,
// talos.tf, three manifests and two epoch records, that account is written from
// the Go and the HCL and is then asked to adjudicate several thousand lines of
// narrative about tailnets, Cilium and CoreDNS. It cannot. And against a
// summary of something else, EVERY specific claim reads as an addition - which
// is why the findings were manufactured at the right altitude from the wrong
// document, and why the more carefully a file was commented the more of them it
// attracted.
//
// So the fix is narrowing, as the note said, and it costs nothing: the blind
// pass now returns one account PER FILE in the same single call, and this pass
// is handed them paired - the account of a file beside the commentary of that
// same file, and nothing else. A file that contributed no code to the account,
// which is every Markdown record by construction, is not compared at all and is
// named in the run note instead of being judged against an account of the Go
// beside it.
const comparePrompt = `Below are files. For each one you are given an account of what THAT FILE does, written by someone who read it with every comment removed, and beside it the commentary that was actually written about THAT FILE.

Report only where a file's commentary makes a claim that ITS OWN account CONTRADICTS. For example: a comment describing a retry where the account describes no retry; a doc string naming a parameter the account says the function does not take.

NEVER compare one file's commentary against another file's account. They are separate subjects and a disagreement between them is not a disagreement about anything. Each block below is self-contained; judge it on its own and move on.

Three things are never findings.

1. SILENCE. If the account simply does not mention what the commentary describes, that is not a disagreement. The account is a summary, not an inventory, and it being quiet about something is not evidence against it. "The account does not mention X" is a sentence to delete rather than to report.

2. TEXT THAT IS NOT A CLAIM ABOUT THIS CODE. The commentary was separated from the code mechanically, so it contains things that are not commentary at all: fixture data, sample payloads, quoted command output, example diffs, and text deliberately describing something that does not exist so that a test can fail on it. A line that reads like prose but is data asserts nothing about the code. Leave it alone.

3. CLAIMS ABOUT ANYTHING THE ACCOUNT CANNOT SEE. The account describes this code as it is now, and nothing else. Commentary routinely explains the boundary around it: what a different file owns, which tool runs elsewhere, what an earlier version of this code did, why something was changed. None of that can be confirmed or contradicted by an account of the code as it stands. A comment explaining what the code used to do is context, not a disagreement.

Judge only against the account. You have not seen the code and must not guess at what it might also do.

Every finding must name the specific claim in the commentary AND the specific statement in the account that contradicts it. If you cannot quote both, there is no finding.

Use the rule "commentary-disagrees" for every finding, and cite the exact line number of the commentary sentence you are quoting - a finding pointing at a line that does not contain the claim costs the reader more than it saves.
` + findingRules

// handoverPrompt asks the question tests/go/repo/forkable_test.go cannot.
//
// That test enforces forkability by pattern and catches literal names. It
// cannot catch a step assuming an account already exists, a runbook missing a
// prerequisite, or a config key with no instruction for what to put in it.
// Reading as a stranger IS the task, which is the one place where having no
// context is the qualification rather than the limitation.
const handoverPrompt = `You intend to run this project yourself, against your own accounts and your own hardware. You have never spoken to anyone who built it, and you cannot ask.

WHAT YOU ARE LOOKING AT. You have NOT been given the repository. You have been given only the files one change touched. The repository is much larger and everything else in it exists; you simply cannot see it.

So three things are never findings, and each is a fact about your input rather than about the work:

1. THAT SOMETHING DOES NOT EXIST. You cannot tell. A file, function, type, test, script or directory named in the text you were given is almost certainly there and simply outside your view. Never write that the repository "contains no such file" or that something "does not exist".

2. THAT SOMETHING IS UNDEFINED, UNEXPLAINED OR UNDOCUMENTED because you cannot find where it comes from. A function called here is defined somewhere you were not shown - very often in the same package, in a file this change did not touch. Not seeing a definition is not evidence there is none.

3. THAT A CROSS-REFERENCE IS WRONG. When the text points at another path, you cannot check it. Take it at face value.

What you CAN judge is whether the files in front of you tell somebody what to DO: whether a step assumes an account, credential or machine exists without saying how to obtain one; whether a configuration key says what to put in it; whether an order of operations is stated rather than implied.

ORDINARY INTERNET ACCESS IS NOT ONE OF THOSE THINGS. A script that downloads a tool from its vendor is doing its job, and "this assumes the internet is reachable" is not a finding. Neither is pulling a public container image. What would be a finding is a step needing an account, a licence or a credential to reach something - a private registry, a paid API - with nothing saying how to get one.

ONE FINDING PER GAP, NOT ONE PER LINE. When the same gap repeats down a file, report it once, against the first line where it appears, and say it applies throughout. Ten alerts making one point bury every other finding in the run and get read as noise rather than as ten problems.

Report only what would actually block you, and only from what you were given. Use the rule "handover-gap" for every finding.
` + findingRules + `
The files follow, with line numbers.
`
