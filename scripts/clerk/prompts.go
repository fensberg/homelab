package main

// What the clerk is asked. One prompt per verb, and none of them shows it what
// we claim.
//
// The question is deliberately never "where do the docs and the code
// disagree". That builds an accusation engine, and this estate already
// rejected reviewers whose wrong findings cost more than their right ones.
// Asking a reader with no context to describe what it sees removes that class
// of failure rather than filtering it: a description cannot be wrong in the
// corrosive way an accusation can, and where it misreads, the misreading is
// itself the finding.

// The rules every verb inherits.
//
// The citation rule is what makes a finding checkable in ten seconds instead
// of adjudicable in ten minutes, and it is also what keeps the clerk to
// falsifiable claims: an assertion that names a file, a line, a flag or a
// command can be checked, and "the design is elegant" cannot.
const commonRules = `
Rules that apply to everything you write:
- Describe only what is in front of you. Do not guess at intent you cannot see.
- Every claim must cite a file and a line, as path:line. A claim you cannot cite, do not make.
- If something is unclear or looks wrong to you, say so plainly and cite it.
- Do not praise, do not summarise your own answer, and do not offer to help further.
- Be brief. A short account somebody reads beats a long one they skim.
`

// auditPrompt asks for the clerk's own account of what some code does.
const auditPrompt = `You are reading part of a repository you have never seen before and know nothing about.

Write a plain account of what this code does: what it is for, what it would do when it runs, and anything a stranger would need to know to change it safely.
` + commonRules + `
The files follow.
`

// handoverPrompt asks the question tests/go/repo/forkable_test.go cannot.
//
// That test enforces forkability by pattern and catches literal names. It
// cannot catch a step that assumes an account already exists, a runbook
// missing a prerequisite, or a config key with no instruction for what to put
// in it. Reading as a stranger IS the task here, which is the one place where
// having no context is not a limitation but the qualification.
const handoverPrompt = `You have just cloned this repository and intend to run it yourself, against your own accounts and your own hardware. You have never spoken to anyone who built it.

Work out what you would have to do, and report what you cannot work out. Look for:
- steps that assume an account, a credential, a network or a machine already exists, without saying how to get one
- configuration keys with no instruction for what to put in them
- instructions that name something the repository does not explain
- an order of operations that is implied but never stated

Report what would stop you, not what is merely unfamiliar.
` + commonRules + `
The files follow.
`
