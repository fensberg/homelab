# Patches for protected paths

Changes the agent prepared and cannot push itself.

`.github/workflows/**` is behind the agent boundary: the GitHub App has no
`workflows` permission, deliberately, so that nothing automated can alter the
mechanisms that reach real hardware. That decision is not up for revisiting -
see [`docs/epochs/01-ignition.md`](../../docs/epochs/01-ignition.md) - but it
does mean a workflow change has to be handed over rather than published.

**Handing over a diff is not handing over an action.** A diff has no line
numbers, cannot be pasted into a shell, and leaves the reader to find what it
replaces. So the patch lives here as a file, and applying it is one command
run from the repository root:

```sh
task apply-patches
```

It applies every outstanding patch and removes each one, **all or none**. Check
the result with `git diff --cached`, then commit and push as you would any
change; it deliberately does not do either for you.

Underneath it is the same pair of commands, per patch:

```sh
git apply --index .github/patches/<name>.patch && git rm .github/patches/<name>.patch
```

**Do not run that chain by hand across several patches.** `&&` stops at the
first failure and leaves whatever already applied staged, which is the worst
outcome because it looks finished. That happened: a patch was renamed between
the instruction being written and being run, the chain half-applied, and the
branch diverged from its remote with a commit nobody could see the shape of.
`task apply-patches` checks every patch applies BEFORE applying any, and
refuses a dirty tree - because a half-applied patch from a previous attempt
cannot be told apart from work in progress.

`--index` because a patch can add or rename a file, and plain `git apply`
writes a new file into the working tree without staging it. The commit that
follows then carries the edits and silently omits the new workflow - a
half-applied patch that looks whole. With `--index` everything the patch
touches is staged, renames included, and it also refuses to apply over
uncommitted edits to those files rather than mixing them in.

One command, and it finishes the job. Applying and then remembering to delete
is two steps where one will do, and the second is exactly the kind a computer
should carry rather than a person.

**Everything that changes along with the workflow goes in the same patch** -
the tests that read it, the suppliers list, the mutation ledger, the docs. The
branch is then the old tree until the patch is applied and the new tree
afterwards, never a mix of the two, and the tests read the tree as it is.
Splitting them leaves the old tests judging the new workflow, or the reverse.

Every patch here is verified against a clean tree before it is committed:
`git apply --check` passes, and where it changes a workflow, the result is
confirmed to parse.

That removal is not tidiness. A patch left behind is indistinguishable from one
still outstanding, so the next person reading this directory cannot tell what
has been applied - which is why it is chained to the apply rather than
mentioned underneath it.
