#!/usr/bin/env python3
"""Report code that follows a JavaScript module's export statement.

Reads a JS/TS source file on stdin. If the file has an `export default` or
`module.exports` statement and there is non-comment code after that statement
*ends*, prints `<start-line>|<total-lines>|<first offending line>` and exits 0.
Prints nothing otherwise.

This is the detector for the PolinRider campaign's signature: an obfuscated
payload appended to the bottom of a legitimate config file, below where a
reviewer stops reading. It deliberately carries no indicator list, so it keeps
working against variants whose strings nobody has published yet.

The brace tracking is the whole point. An export statement routinely spans many
lines — `module.exports = { a, b, c };` — and treating the line it starts on as
its end flags every multi-line export in a normal repository. A check that
fires on honest code is a check people learn to click past, which is how the
real thing gets through.
"""
import re
import sys

# Strings and line comments are removed before counting brackets, so a brace
# inside a string literal cannot unbalance the depth.
_NOISE = re.compile(
    r'"(?:\\.|[^"\\])*"'      # double-quoted
    r"|'(?:\\.|[^'\\])*'"     # single-quoted
    r"|`(?:\\.|[^`\\])*`"     # template literal
    r"|//.*$"                 # line comment
)

_EXPORT = re.compile(r"\s*(?:export\s+default|module\.exports)\b")
_COMMENTISH = re.compile(r"\s*(?://|\*|/\*|\*/)")

_OPEN = "{(["
_CLOSE = "})]"


def trailing_code(text):
    lines = text.split("\n")

    start = None
    for i, line in enumerate(lines):
        if _EXPORT.match(line):
            start = i  # the last export wins; a file may re-export
    if start is None:
        return None

    depth = 0
    end = start
    for i in range(start, len(lines)):
        stripped = _NOISE.sub("", lines[i])
        depth += sum(stripped.count(c) for c in _OPEN)
        depth -= sum(stripped.count(c) for c in _CLOSE)
        end = i
        if depth <= 0:
            break

    for line in lines[end + 1:]:
        if line.strip() and not _COMMENTISH.match(line):
            return start + 1, len(lines), line[:130]
    return None


def main():
    found = trailing_code(sys.stdin.read())
    if found:
        print("%d|%d|%s" % found)


if __name__ == "__main__":
    main()
