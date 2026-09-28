# Contributor issue backlog (draft)

Drafted for review before any of these become GitHub issues. None has been filed. Each comes from a gap
found while building v0.1.0, and says where it was found, what done looks like, and what evidence the
change needs. The rules in [CONTRIBUTING.md](../CONTRIBUTING.md) apply to all of them; in particular, any
change that makes the tool claim more is a snapshot diff that must be explained.

## 1. Render muxed addresses instead of marking them opaque

**Found:** SEP-41 v0.5.2 declares `transfer`'s recipient as `MuxedAddress` ("A MuxedAddress or Address").
This library renders every address through `soroauth.FormatAddress`, which refuses the muxed arm, so a
`transfer` to a muxed recipient renders `opaque` (`TestExplainTokenMuxedRecipient`).

**Done when:** a muxed recipient renders as its `M…` strkey, and a transfer to one can be `decoded` on a
derived SAC.

**Constraint:** the brief requires addresses to go through `soroauth.FormatAddress`, never a second
encoder. This probably needs a muxed formatter in soroauth-go first, and a re-pin, which has its own
conditions (brief section 0). Start by opening the discussion there.

**Evidence:** snapshot cases for a muxed recipient on a derived SAC and on an unidentified contract, and a
live run that includes one, checked against the host event's `to_muxed_id`.

## 2. Name arguments from a contract's own spec (optional, networked)

**Found:** most real entries are `opaque` because their functions are outside SEP-41 (see
[docs/EVIDENCE.md](EVIDENCE.md): `set_price`, `plant`, `swap`, `batch_work`). A contract's wasm carries a
spec that names its functions' parameters.

**Done when:** an option, behind `context.Context`, fetches a contract's spec and names the arguments of
an otherwise opaque call. Its absence or failure degrades to today's output and never errors.

**Constraint:** a spec names arguments; it does not say what the contract does with them. Named
arguments move a node from `opaque` to at most `partial`, never to `decoded`. No registry service, no
lookup by name (brief sections 1 and 6.6).

**Evidence:** snapshot cases with a recorded spec; a live run showing the change in the opaque share.

## 3. Fuzz the decoder and the explainer

**Found:** decoding and explaining take hostile input, and today they are covered by table tests only.

**Done when:** fuzz targets exist for the CLI's `decodeEntry` and for `Explain` over decoded entries. The
invariants are no panic, no result beyond the limits, confidence equal to the floor of its nodes, and a
non-decoded explanation always has reasons. Seed corpora come from `testdata/entries`.

**Constraint:** seeds run in the ordinary suite; the fuzzing itself runs in `ci-extra.yml`, never in the
pull-request checks (brief section 11.7). Prove the corpus is read: the case names must appear in
`go test -v` output (section 11.5).

## 4. Exercise zsh completion behaviour, not only its syntax

**Found:** bash and fish scripts are checked by asking each shell what it would complete
(`TestBashCompletionBehaviour`, `TestFishCompletionBehaviour`). The zsh script is only parsed with
`zsh -n`.

**Done when:** a test loads the zsh script into a zsh with `compinit` and checks the offered completions
at the same positions as the bash and fish tests, running in CI where zsh is installed.

## 5. Check decoded actions from failed transactions in-repo

**Found:** in the public-network run, 48 decoded actions sat in failed transactions, which emit no events,
so the live check reports `no-events` for them. For CP4 they were cross-checked outside the repository
with a second implementation (`@stellar/stellar-sdk` 17.1.0 decoding the same entries), and all 329
decoded actions across both runs matched. That check is not reproducible from the repository.

**Done when:** a reproducible second-implementation check exists, for example a Node script run by
`ci-extra.yml`, and the evidence document reports its result for every decoded action.

**Constraint:** it must not share code with the explanation path. It adds a Node toolchain to CI, so it
belongs in the advisory workflow only.

## 6. Cover the SEP-41 functions the live runs have not seen

**Found:** the recorded runs contain decoded `transfer`, `approve` and `burn` actions, but no decoded
`transfer_from` or `burn_from`. Their live checks exist in `live_test.go` but have not been exercised
against real traffic.

**Done when:** a recorded run includes at least one decoded `transfer_from` and one `burn_from`, each
with a passing host-event check. If sampling does not find them, document the search rather than
constructing traffic.

## 7. Check Stellar Asset Contract deployments against the ledger

**Found:** `create_contract` from an asset, with the `stellar_asset` executable, renders `decoded`
("Deploy the Stellar Asset Contract for …"). The live contract-creation check handles wasm contracts
created from an address only, and reports anything else as `not-checked`.

**Done when:** the live check derives the SAC ID for the rendered asset and confirms the ledger holds a
`stellar_asset` instance there whose `AssetInfo` matches, as `TestSACContractIDMatchesNetwork` does for
the recorded fixtures.

## 8. Interpret executable tags and external-reference executables, or keep them opaque on purpose

**Found:** `ScvExecutableTag` values and `CONTRACT_EXECUTABLE_EXTERNAL_REF` executables render `opaque`,
because v0.1.0 did not read the protocol text that defines them (`scval.go`).

**Done when:** either they are interpreted with a cited CAP and section, and snapshot cases, or the code
comment names the CAP and explains why they stay opaque.

## 9. Measure test coverage before setting any floor

**Found:** no coverage measurement exists. The sibling project documented one floor while enforcing
another (brief section 11.3).

**Done when:** coverage is measured and reported, with the date, and any floor set in CI sits below the
measured value, with the measurement recorded next to it.

## 10. Consider a smaller JSON rendering for delegates

**Found:** each delegate in `render.JSON` repeats the entry's full action tree, because a delegate signs
the same payload and must carry its confidence. For soroauth-go's vector 6 that is about 450 lines of
JSON for one transfer.

**Done when:** there is a decision, with the wallet use case in mind: either keep the repetition, which
lets a delegate be shown on its own, or reference the entry's actions. Any change is a wire-format change
and needs a CHANGELOG entry marked breaking.
