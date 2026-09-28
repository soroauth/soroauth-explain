# Contributing to soroauth-explain

This tool tells people what they are about to authorize. A wrong `decoded` rendering is the worst bug it
can have, because a reader acts on it. Most of the rules below exist to make every change in what the tool
claims visible and reviewable.

## Setup

You need Go at the version in `go.mod` (the `toolchain` line; `GOTOOLCHAIN=auto` fetches it) and git.

```sh
git clone https://github.com/soroauth/soroauth-explain
cd soroauth-explain
go test ./...
```

Before sending a change, run what the required CI job runs:

```sh
gofmt -l .
go vet ./...
go vet -tags live ./...
go test ./...
```

`gofmt -l .` must print nothing.

The completion tests parse the bash, zsh and fish scripts with each shell. A shell you do not have is
reported as skipped. CI installs all three and sets `SOROAUTH_REQUIRE_SHELLS=1`, which turns a missing
shell into a failure. To do the same locally (it fails unless bash, zsh and fish are all installed):

```sh
SOROAUTH_REQUIRE_SHELLS=1 go test -run Completion ./cmd/soroauth-explain
```

## Continuous integration

Pull requests run two checks, both in `.github/workflows/ci.yml`:

- **vet and test**: gofmt, `go vet` with and without the `live` tag, `go test ./...` with every
  completion shell required.
- **snapshots are reproducible**: regenerates every snapshot input and rendering and fails if anything
  differs from what is committed.

`.github/workflows/ci-extra.yml` runs the live tests against testnet and the public network on push to
`main` and on demand. It is advisory and never runs on a pull request.

## Snapshots: never edit one by hand

Every rendering is pinned by a committed file in `testdata/snapshots/`, one per case per format, generated
from the inputs in `testdata/entries/`. `TestSnapshots` requires byte equality.

**A snapshot is never edited by hand.** If output changes, the code changed. Regenerate:

```sh
go run ./cmd/gensnapshots
```

The generator refuses to run with a dirty working tree, so a regeneration cannot carry unrelated changes.
Commit your code change first, run the generator, then add the regenerated files to that same commit
before you push it. The generator also rewrites `testdata/entries/`: inputs copied from soroauth-go's
golden vectors at the version `go.mod` pins, and the built cases defined in `cmd/gensnapshots/main.go`.
To add a case, add it there, not as a hand-written file.

**A pull request that changes a snapshot must say in its body what changed in the rendering and why.** A
snapshot diff with no explanation cannot be reviewed and will be closed. Changing a case from `opaque` to
`decoded` is exactly the kind of change that needs this.

## Adding a registry entry

A registry entry is what lets the tool say what a function call means. It is the most sensitive change
you can make, because it is how `opaque` becomes `decoded`.

What it needs:

1. **A published standard, cited.** Registry entries live in `interfaces/`. Cite the document (for
   example a SEP) by number, section and the git commit you read it at, in the doc comment.
2. **The standard's text, committed and compared.** Commit the verbatim interface text as a fixture, and a
   test that parses it and compares every registered parameter's name and type with it. See
   `interfaces/testdata/sep-0041-interface.rs` and `TestSEP41MatchesTheSEP`. A signature written from
   memory must fail that test.
3. **Matching by name, arity and argument types.** A name alone is never a match.
4. **`decoded` only through a checkable rule.** A match says what the arguments are, not what the contract
   is. An entry may render `decoded` only when every element it states is derived: the asset from a
   derived Stellar Asset Contract ID, the decimals from a documented constant for that contract type.
   Anywhere else it is at most `partial`, with raw values and an `Unexplained` sentence. Each summary
   template must name every argument.
5. **Snapshot cases for each path.** At least the `decoded` path, the `partial` path (the same call on a
   contract that is not identified), and an impostor: a contract with the right signature that must not
   earn the meaning.
6. **Live evidence.** Record fresh live runs and regenerate the evidence document (see below). Every new
   `decoded` action must pass its independent check against the host's events or ledger state. If a new
   kind of action has no such check, add one to `live_test.go` in the same change.

## Live evidence

`docs/EVIDENCE.md` is generated from the recorded runs in `testdata/live/`. `TestEvidenceUpToDate`
explains every recorded entry again and fails if the current code does not say exactly what was
recorded. A change to what the tool says about real entries therefore needs new recordings:

```sh
SOROAUTH_LIVE_RECORD=1 go test -tags live -run 'TestLive(Testnet|Public)' -v -count=1 -timeout 30m .
SOROAUTH_WRITE_EVIDENCE=1 go test -run TestEvidenceUpToDate .
```

The live tests reach the network, so they only build with `-tags live`. No other test touches the
network. Keep samples as they come; do not look for friendlier data.

## Commits and pull requests

- Conventional commits, lowercase and imperative: `feat(explain): ...`, `fix(cmd): ...`,
  `docs: ...`, `test(live): ...`, `ci: ...`.
- One logical unit per commit: one function and its tests, one subcommand, one document.
- Name the files you stage. Do not use `git add .` or `git add -A`.
- Never force-push, and never rewrite pushed history.
- A commit that corrects an earlier wrong claim or wrong code is its own commit. Its body says what was
  wrong, how it was found (with the command and its output), and what changed.
- Every factual claim in a commit, document or pull request comes with the command you ran and its output,
  or the file and line you read.
- Do not describe an explanation as safe, verified or trusted. It is `decoded`, `partial` or `opaque`.
- Before committing a Markdown change, check the code-fence count is even and the file ends in a newline.

## Adding a CLI flag or subcommand

A flag must be added to the completion spec in `cmd/soroauth-explain/completions.go`, or
`TestCompletionSpecMatchesFlags` fails. A subcommand must be wired into the dispatcher in `main.go`, the
usage text and the completion spec in the same commit, with a test that reaches it through `run`. If the
synopsis changes, update the README too: `TestReadmeUsage` compares them.

## Reporting a security problem

Do not open a public issue. See [SECURITY.md](SECURITY.md).
