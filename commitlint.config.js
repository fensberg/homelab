// Conventional Commits, nothing customized yet - the bones to build on, not
// the finished shape. Enforced twice, same pattern as formatting: this file
// is what Super-Linter's already-present GIT_COMMITLINT validator needs (it
// was silently no-op-ing without one - see its own warning in a CI log), and
// alessandrojcm/commitlint-pre-commit-hook (.pre-commit-config.yaml) reads
// this same file for the local commit-msg hook, so there is one rule, not
// two copies of it.
module.exports = {
  extends: ["@commitlint/config-conventional"],
  helpUrl: "https://www.conventionalcommits.org/",
  // Dependabot's own commit messages don't follow this convention and are
  // not something we write - not worth failing CI over.
  ignores: [(msg) => /^Bump /.test(msg)],
};
