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
git apply .github/patches/<name>.patch && git rm .github/patches/<name>.patch
```

One command, and it finishes the job. Applying and then remembering to delete
is two steps where one will do, and the second is exactly the kind a computer
should carry rather than a person.

Every patch here is verified against a clean tree before it is committed:
`git apply --check` passes, and where it changes a workflow, the result is
confirmed to parse.

That removal is not tidiness. A patch left behind is indistinguishable from one
still outstanding, so the next person reading this directory cannot tell what
has been applied - which is why it is chained to the apply rather than
mentioned underneath it.
