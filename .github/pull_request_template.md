## What this changes

<!-- One or two sentences. If it fixes an issue, link it. -->

## Why

<!-- What was wrong, or what could not be done before. -->

## Evidence

<!--
Commands you ran and their real output, or the file and line you read. Claims about protocol or host
behaviour need a source: the SEP or CAP and section, or the rs-soroban-env or SDK file and line. Not
memory.
-->

## Rendering changes

<!--
Required if any file in testdata/snapshots/ or docs/EVIDENCE.md changed. For each changed case: what the
rendering said before, what it says now, and why the new output is correct. A snapshot diff with no
explanation will be closed. Delete this section only if no rendering changed.
-->

## Checklist

- [ ] `gofmt -l .` prints nothing; `go vet ./...`, `go vet -tags live ./...` and `go test ./...` pass
- [ ] No snapshot or evidence file was edited by hand; they were regenerated with `go run ./cmd/gensnapshots` or the live commands in CONTRIBUTING.md
- [ ] Nothing renders `decoded` unless every element is derived from the bytes or a checkable rule
- [ ] No contract is labelled with an asset except through a derived Stellar Asset Contract ID
- [ ] No amount is scaled without a checkable decimal count
- [ ] If this adds a guard, I broke it deliberately, saw the test fail, said so above, and did not commit the break
- [ ] New CLI flags are in the completion spec
- [ ] Markdown changes have an even number of code fences and end with a newline

## Wire format

- [ ] This does not change a field name in the JSON rendering.
- [ ] It does, and CHANGELOG.md lists it as a breaking change.
