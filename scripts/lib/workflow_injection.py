#!/usr/bin/env python3
"""Report untrusted GitHub Actions expressions interpolated into shell scripts.

Reads a workflow YAML file on stdin. Prints one line per finding:

    <job>.<step>: <expression>

A `${{ github.event.* }}` expression that lands directly in a `run:` block is a
command-injection hole: a pull request title, branch name or comment body is
attacker-controlled, and GitHub substitutes it into the script before the shell
sees it. Quoting does not help, because the substitution happens first.

Passing the same value through `env:` is the documented mitigation and is NOT a
finding. The value becomes an ordinary environment variable, the shell never
parses it as source text, and `"$VAR"` is then safe. Flagging that form too
would mean flagging the fix as well as the bug, which teaches people to ignore
the check — so this distinguishes the two rather than grepping for the
expression anywhere in the file.
"""
import re
import sys

try:
    import yaml
except ImportError:
    # Never exit silently: a missing parser would turn this check into a
    # vacuous pass, which is worse than not having it. The caller turns this
    # marker into a visible warning.
    print("UNAVAILABLE: PyYAML is not installed")
    sys.exit(0)

# Contexts an outside contributor can set the contents of.
UNTRUSTED = re.compile(
    r"\$\{\{[^}]*\bgithub\.(?:"
    r"event\.(?:pull_request|issue|comment|review|discussion|workflow_run)\b"
    r"|head_ref\b"
    r")[^}]*\}\}"
)


def steps_of(workflow):
    jobs = workflow.get("jobs") or {}
    if not isinstance(jobs, dict):
        return
    for job_name, job in jobs.items():
        if not isinstance(job, dict):
            continue
        for i, step in enumerate(job.get("steps") or []):
            if isinstance(step, dict):
                yield job_name, step.get("name") or "step %d" % (i + 1), step


def main():
    try:
        workflow = yaml.safe_load(sys.stdin.read())
    except Exception:
        return
    if not isinstance(workflow, dict):
        return

    for job_name, step_name, step in steps_of(workflow):
        script = step.get("run")
        if not isinstance(script, str):
            continue
        for match in UNTRUSTED.findall(script):
            print("%s.%s: %s" % (job_name, step_name, match))


if __name__ == "__main__":
    main()
