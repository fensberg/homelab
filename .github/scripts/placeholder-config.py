#!/usr/bin/env python3
"""Render config/management.tpl.json with placeholders instead of secrets.

`tofu validate` reads the rendered config through jsondecode(file(...)), so it
cannot run without one - and CI has no 1Password credentials, deliberately.
This writes a stand-in with the same shape and no real values.

It is not only a means to an end. Because the placeholders are typed by key,
this fails when the template and the OpenTofu that reads it disagree: add a key
to variables.tf that the template does not declare, and validate says so on the
pull request rather than on the night of a deployment.
"""

import json
import re
import sys

TEMPLATE = "config/management.tpl.json"
RENDERED = "config/management.rendered.json"

SENTINEL = "__OP_REFERENCE__"

# Some values have to be shape-correct or a provider rejects them while
# validate is still evaluating its configuration block. The flux provider is
# the one that does this today: it checks that the repository URL has a scheme
# it recognises before anything else happens.
URL_KEYS = ("repo_url", "endpoint", "url")


def placeholder_for(key):
    """Pick a stand-in that will survive whatever reads this key."""
    if key in URL_KEYS or key.endswith("_url"):
        return "https://example.invalid/placeholder"
    return "placeholder"


def walk(node, key=""):
    if isinstance(node, dict):
        return {k: walk(v, k) for k, v in node.items()}
    if isinstance(node, list):
        return [walk(v, key) for v in node]
    if node == SENTINEL:
        return placeholder_for(key)
    return node


def main():
    with open(TEMPLATE, encoding="utf-8") as fh:
        raw = fh.read()

    # Mark every op:// reference first, then decide what to put in its place
    # once the JSON is parsed and each one's key is known.
    marked = re.sub(r"\{\{\s*op://[^}]*\}\}", SENTINEL, raw)

    try:
        parsed = json.loads(marked)
    except json.JSONDecodeError as exc:
        print(f"{TEMPLATE} is not valid JSON once injected: {exc}", file=sys.stderr)
        return 1

    with open(RENDERED, "w", encoding="utf-8", newline="\n") as fh:
        json.dump(walk(parsed), fh, indent=2)
        fh.write("\n")

    print(f"wrote {RENDERED}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
