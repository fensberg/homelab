// Conventional Commits, plus one local rule. Enforced twice, same pattern as
// formatting: this file is what Super-Linter's already-present GIT_COMMITLINT
// validator needs (it was silently no-op-ing without one - see its own
// warning in a CI log), and alessandrojcm/commitlint-pre-commit-hook
// (.pre-commit-config.yaml) reads this same file for the local commit-msg
// hook, so there is one rule, not two copies of it.

// Claude commits to this repository under its own git identity, so a
// `Co-Authored-By: Claude` trailer credits the same party twice - GitHub
// renders such a commit with an author avatar and a redundant co-author
// badge. The trailer exists to credit a contributor who is NOT the author.
//
// This is a rule rather than a note because the Claude Code harness instructs
// the model to append that trailer by default, on the assumption that a human
// is committing the model's work. Here the reverse is true, so the correction
// has to sit somewhere a fresh session cannot miss it - and a comment in
// CLAUDE.md is exactly the kind of thing that gets missed.
//
// Matched on the vendor domain as well as the name, so renaming the model
// does not quietly reopen it. Scoped to the trailer: this repository writes
// the word "Claude" in prose constantly, and only the attribution is wrong.
const selfCoAuthorship = /^\s*co-authored-by:[^\n]*(claude|anthropic\.com)/im;

module.exports = {
  extends: ["@commitlint/config-conventional"],
  helpUrl: "https://www.conventionalcommits.org/",
  // Dependabot's own commit messages don't follow this convention and are
  // not something we write - not worth failing CI over.
  ignores: [(msg) => /^Bump /.test(msg)],
  plugins: [
    {
      rules: {
        "no-self-co-authorship": ({ raw }) => [
          !selfCoAuthorship.test(raw ?? ""),
          "Drop the `Co-Authored-By: Claude` trailer. Claude is the author of " +
            "this commit, not a co-author, and the trailer makes GitHub show " +
            "the same party twice. A trailer naming a human co-author is fine.",
        ],
      },
    },
  ],
  rules: {
    "no-self-co-authorship": [2, "always"],
  },
};
