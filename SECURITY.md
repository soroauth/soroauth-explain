# Security policy

## Reporting a vulnerability

**Do not open a public issue.**

Report privately through GitHub Security Advisories:
[github.com/soroauth/soroauth-explain/security/advisories/new](https://github.com/soroauth/soroauth-explain/security/advisories/new).

Include what you can: the version or commit, the entry as base64 XDR, the options or CLI flags used, what
the tool rendered, and what it should have rendered.

You will get an acknowledgement within a week. This is a small project with no paid on-call, so that is a
realistic commitment rather than an optimistic one. If a fix is needed you will be credited in the advisory
and the changelog unless you ask otherwise.

## Severity

### Critical: a wrong `decoded` rendering

People read this tool's output and decide whether to approve an authorization. A rendering marked
`decoded` is presented as fact, and a user will act on it. **Any `decoded` rendering that is wrong is a
critical bug**, including:

- a contract labelled with an asset it is not the derived Stellar Asset Contract of, on that network (the
  impostor guard failing);
- a wrong amount, including an amount scaled by a number of decimal places the contract has not been shown
  to use;
- a wrong party (sender, recipient, spender, deployer), function, contract or ledger number;
- anything rendered `decoded` that should have been `partial` or `opaque`.

Also critical:

- an explanation whose confidence is higher than its least certain node;
- an `opaque` or `partial` node omitted from a rendering, or an explanation that is not `decoded` but gives
  no reason under "Not determined";
- a rendering that hides part of what an entry authorizes.

### High

- A panic, hang or unbounded resource use on attacker-supplied input: a crafted entry that escapes the
  decode limits (1 MiB, nesting depth 64) or the walk limits (`DefaultMaxDepth`, `DefaultMaxNodes`).
- Contract-supplied text that reaches a rendering unescaped, such as terminal control sequences or
  newlines that could make one line pass for another.
- The CLI writing anything other than a result to stdout, which a caller piping `--json` into another tool
  would read as output.

### Not a vulnerability

- An entry rendered `opaque` or `partial`. That is the tool reporting what it cannot establish. Requests
  to interpret more functions are feature requests.
- What a transaction will do or cost. This tool explains what an entry authorizes and nothing more.

## Supported versions

Fixes go into the latest `v0.x` release. The library is unaudited.
