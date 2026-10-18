# The code rules

Each file here is a set of Semgrep rules, held by `TestTheCodeRulesHold`, which
runs them over every file of a kind `tests/languages.yml` hands to Semgrep. Each
is proved by its examples in `tests/`, run by `TestEveryCodeRuleProvesItself`
with `semgrep --test`. Both run with inline ignores off (`--disable-nosem`): an
estate rule that is wrong is changed, with its examples, not silenced where it
fires.

What took a round each to learn, so the next rule does not:

- **One example file per rule file, annotating every rule in it.** `--test`
  pairs `x.yml` with `tests/x.<ext>`, and refuses an example that does not
  mention every rule of its pair. Rules over two languages are two files.
- **`--test` ignores `paths:`.** An example proves a pattern, never its scope.
  Scope is proved in the ledger, place by place: every include and every
  exclude needs a proof that breaks a file there and names the rule with
  `rule:` (`TestEveryPlaceACodeRuleReadsIsProved`).
- **Every rule shows a near miss** with an `ok:` line, not only the faults it
  refuses (`TestEveryCodeRuleShowsWhatItAccepts`).
- **An example is not the estate.** Neither harness reads `policy/semgrep/`,
  and an example that declares something real-looking - a secret, a resource -
  would otherwise be taken for it.
- **`metavariable-regex` is anchored at the start.** Write `.*` in front to
  search.
- **A generic `pattern-regex` restarts after a rejected match.** A lookahead
  that refuses `example-cp-1` lets the engine try `cp-1` next. Anchor the start
  with a lookbehind rather than `\b`.
- **Semgrep lists files through git.** A rule or example that is not yet
  tracked is not seen: `git add -N` it before running.
- **A rule whose matches are expected, not refused, says so** with
  `metadata: {presence: <why>}`. The harness fails when it finds nothing.
- **Some readings belong to Conftest.** Counting ("declared exactly once"), a
  file's own path beside its contents, or JSON, which has no comments for an
  annotation, are configuration rules in `policy/conftest/`.
