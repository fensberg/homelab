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
git apply .github/patches/<name>.patch
```

Every patch here is verified against a clean tree before it is committed:
`git apply --check` passes, and where it changes a workflow, the result is
confirmed to parse.

Delete a patch in the same commit that applies it. One left behind is
indistinguishable from one still outstanding.
