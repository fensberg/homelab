package main

// What the clerk is asked.
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
`

// blindPrompt reads the work without being told anything about it.
//
// The commentary has been blanked out, which is what makes the structural half
// honest: a comment explaining why a block exists is exactly what stops a
// reader noticing that nothing reaches it.
const blindPrompt = `You are reading source from a repository you have never seen. All comments have been removed. Nobody has told you what any of it is for, and you must not assume there is a good reason for anything.

Do two things.

First, work out what this code actually does, and write it down plainly. This is for your own use in a later step; keep it short.

Second, list what is not built soundly. Look for things like:
- code nothing reaches, or a branch that cannot be taken
- a part of a file that connects to nothing else in it
- a function that calls itself, or a value written and then read by nobody but the thing that wrote it
- the same work done twice in two places
- a name that says one thing while the thing beside it does another
- an error that is swallowed, or a failure path that cannot be reached
- structure so tangled that you cannot follow what happens when

Use the rule "unsound-work" for every finding.

Answer with JSON and nothing else, as one object:

  {"account": "<what the code does, plainly>", "findings": [ ... ]}
` + findingRules + `
The code follows, with line numbers.
`

// comparePrompt is shown the claim only after the account exists.
const comparePrompt = `Below is an account of what some code does, written by someone who read it with every comment removed. After it is the commentary that was actually written about that code - comments, doc strings and documents.

Find places where the commentary claims something the account does not support. For example: a comment describing a retry where the account describes no retry; a doc string naming a parameter the account never mentions; a document asserting behaviour the account says does not happen.

Judge only against the account. You have not seen the code and must not guess at what it might also do. If the account is silent on something the commentary claims, that is not a disagreement - it is silence, and you should leave it alone.

Use the rule "commentary-disagrees" for every finding, and cite the line of the COMMENTARY that is wrong.
` + findingRules

// handoverPrompt asks the question tests/go/repo/forkable_test.go cannot.
//
// That test enforces forkability by pattern and catches literal names. It
// cannot catch a step assuming an account already exists, a runbook missing a
// prerequisite, or a config key with no instruction for what to put in it.
// Reading as a stranger IS the task, which is the one place where having no
// context is the qualification rather than the limitation.
const handoverPrompt = `You have just cloned this repository and intend to run it yourself, against your own accounts and your own hardware. You have never spoken to anyone who built it, and you cannot ask.

List what would stop you. Look for:
- a step assuming an account, credential, network or machine already exists, without saying how to obtain one
- a configuration key with no instruction for what to put in it
- an instruction naming something the repository never explains
- an order of operations that is implied but never stated

Report what would actually block you, not what is merely unfamiliar. Use the rule "handover-gap" for every finding.
` + findingRules + `
The files follow, with line numbers.
`
