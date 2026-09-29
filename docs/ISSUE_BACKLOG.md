# Contributor issue backlog (draft)

Drafted for review before any of these become GitHub issues. **None has been filed.** Each item comes from
a gap found while building this project, and says where it was found, what done looks like, and what
evidence the change needs. The rules in [CONTRIBUTING.md](../CONTRIBUTING.md) apply to all of them; in
particular, any change that makes the tool claim more is a snapshot diff that must be explained, and
nothing may render `decoded` without a derivation or a published standard behind it.

Complexity tiers:

- **high**: design or research is needed before code, or the change touches what the tool claims
  (confidence, labels, registry entries), and so needs evidence from real traffic.
- **medium**: a well-defined change across more than one file, with tests and usually snapshot cases.
- **low**: a contained change or document with an obvious test.

| Section | High | Medium | Low | Items |
|---|---:|---:|---:|---:|
| Known interfaces (the registry) | 5 | 7 | 3 | 15 |
| Contract specs | 7 | 7 | 5 | 19 |
| Interface families the spec reader now reaches | 5 | 9 | 1 | 15 |
| Evidence and live checks | 2 | 8 | 4 | 14 |
| Fixtures and snapshot cases | 0 | 8 | 10 | 18 |
| Renderers and output | 4 | 7 | 5 | 16 |
| Command-line interface | 2 | 4 | 6 | 12 |
| Robustness, limits and security | 3 | 6 | 3 | 12 |
| Internationalization | 2 | 3 | 4 | 9 |
| Accessibility | 1 | 3 | 4 | 8 |
| Documentation | 0 | 4 | 6 | 10 |
| Project infrastructure | 1 | 2 | 5 | 8 |
| **Total** | **32** | **68** | **56** | **156** |

## Known interfaces (the registry)

### 1. Decode the Stellar Asset Contract admin interface

**Tier:** high

**Found:** Stellar Asset Contracts are the one kind of contract the spec reader cannot name: every wasm contract in both live samples publishes a spec (56 of 56 on testnet, 29 of 29 on public), and the 13 contracts without one are SACs, which run no wasm. They are also the only contracts whose behaviour this library can derive. CAP-46-6 ("Semantics: Admin Interface") defines `set_admin(new_admin: Address)`, `set_authorized(id: Address, authorize: bool)`, `mint(to: Address, amount: i128)` and `clawback(from: Address, amount: i128)`, none of which is registered. The public sample already contains an opaque `mint` on a derived SAC (tx `cc265b25…`, auth 1).

**Done when:** The four admin functions are registered from CAP-46-6's text, with the same verbatim-fixture test SEP-41 has, and render `decoded` only on a derived SAC (for example "Mint 1.0000000 CODE:ISSUER to G…"), `partial` elsewhere.

**Evidence:** Snapshot cases for each function on a derived SAC, on an impostor, and on an arbitrary contract; the SEP-41-style comparison test against the committed CAP text; a live check against the host's `mint`, `clawback` and `set_authorized` events.

### 2. Register SEP-41 read functions, or record why they stay unregistered

**Tier:** medium

**Found:** `interfaces/sep41.go` deliberately leaves `allowance`, `balance`, `decimals`, `name` and `symbol` unregistered, reasoning that an authorization entry naming a read is unusual. That reasoning has not been checked against traffic.

**Done when:** Either the live samples show reads in authorization entries and they are registered (on a derived SAC only), or the doc comment cites the sample that shows they do not occur.

**Evidence:** A count from both live records of entries whose calls are SEP-41 reads.

### 3. Render muxed addresses instead of marking them opaque

**Tier:** medium

**Found:** SEP-41 v0.5.2 declares `transfer`'s recipient as `MuxedAddress`. Every address renders through `soroauth.FormatAddress`, which refuses the muxed arm, so a transfer to a muxed recipient is `opaque` (`TestExplainTokenMuxedRecipient`). This was item 1 of the first backlog.

**Done when:** A muxed recipient renders as its `M…` strkey, and a transfer to one can be `decoded` on a derived SAC.

**Evidence:** The brief requires addresses to go through `soroauth.FormatAddress`, so this probably needs a formatter in soroauth-go and a re-pin, which has conditions (brief section 0). Snapshot cases on a derived SAC and on an unidentified contract; a live case checked against the event's `to_muxed_id`.

### 4. Name the admin-interface arguments of issued-asset SACs with `--asset`

**Tier:** medium

**Found:** Once item 1 lands, an issued-asset SAC's admin calls are decoded only when its asset is a candidate, exactly as transfers are. The CLI supplies candidates with `--asset`, but nothing documents how an issuer reviewing its own `mint` should run the tool.

**Done when:** The README's CLI section shows reviewing an issuer's `mint` with `--asset`, and a snapshot case pins that rendering.

**Evidence:** A snapshot case with the asset as a candidate and one without.

### 5. Decode `transfer` events' muxed data shape in the live check

**Tier:** high

**Found:** SEP-41 documents transfer event data as either `amount: i128` or a map with `amount` and `to_muxed_id`. `eventAmount` in `live_test.go` reads an `amount` key from a map but ignores `to_muxed_id`, so a decoded transfer to a muxed recipient could never be fully checked.

**Done when:** The live check compares `to_muxed_id` with the rendered recipient's muxed ID when present.

**Evidence:** A regression case in `TestLiveCheckRegressions` with a real muxed transfer, found by sampling.

### 6. Decide whether `approve` with amount 0 should say "revoke"

**Tier:** medium

**Found:** SEP-41's `approve` doc says "Set to 0 to revoke the allowance". The summary today reads "Allow X to spend up to 0.0000000 native …", which is accurate but not how a reader would say it.

**Done when:** Either a zero-amount approve renders as a revocation, derived from the amount in the bytes and cited to the SEP's text, or the decision not to is recorded next to the template.

**Evidence:** A snapshot case with amount 0.

### 7. Say when an approval's ledger is already in the past

**Tier:** medium

**Found:** An `approve` renders "until ledger N". SEP-41 says an entry with `live_until_ledger` below the current ledger should be treated as a 0 allowance, but the library is offline and does not know the current ledger.

**Done when:** Decide, and record, whether a caller-supplied current ledger (an option) may add a note such as "ledger N has already passed". It must never change confidence.

**Evidence:** Snapshot cases with and without the option.

### 8. Pin the SEP-41 fixture to a named SEP version in its test

**Tier:** low

**Found:** `TestSEP41MatchesTheSEP` parses `interfaces/testdata/sep-0041-interface.rs`, whose header names v0.5.2 and a commit, but the test does not assert the version.

**Done when:** The test fails if the fixture's header version and the doc comment in `sep41.go` disagree.

**Evidence:** The test failing when either is changed alone.

### 9. Re-read SEP-41 on a schedule and report drift

**Tier:** high

**Found:** The registry is pinned to SEP-41 v0.5.2 at one stellar-protocol commit. The SEP's changelog lists seven versions after v0.1.0 (v0.2.0 to v0.5.2).

**Done when:** An advisory job in `ci-extra.yml` fetches the current SEP text and diffs its interface block against the committed fixture, failing (advisory only) when they differ.

**Evidence:** The job's first run output; a deliberately stale fixture showing it fails.

### 10. Cover `transfer_from` and `burn_from` in live evidence

**Tier:** medium

**Found:** The recorded runs contain decoded `transfer`, `approve` and `burn` actions but no decoded `transfer_from` or `burn_from`, so their live checks exist but have not run against real traffic. This was item 6 of the first backlog.

**Done when:** A recorded run includes at least one of each with a passing host-event check, or the document records the search that found none.

**Evidence:** The live record and the regenerated evidence document.

### 11. Test that every registered template names every argument

**Tier:** low

**Found:** `TestSEP41Templates` checks every SEP-41 template. A second interface added later has no such test unless someone copies it.

**Done when:** The template test iterates over every signature in `interfaces.Default()`, not just `SEP41`.

**Evidence:** The test failing on a deliberately incomplete template in a throwaway registry.

### 12. Show which interface a decoded or partial action matched

**Tier:** medium

**Found:** An action from the registry carries its kind (`token_transfer`) but not the interface (`SEP-41`) or its version, although `interfaces.Signature` knows both.

**Done when:** A field, or an `Unexplained`-style provenance line, names the matched interface and version.

**Evidence:** Snapshot changes explained per section 7; a CHANGELOG entry if the JSON gains a field.

### 13. List registered functions from the CLI

**Tier:** low

**Found:** A user cannot ask the tool which functions it can interpret; they have to read `interfaces/sep41.go`.

**Done when:** `soroauth-explain help interfaces` (or similar) prints the registry, wired through the dispatcher, usage and completions (section 11.1).

**Evidence:** A test through `run()`; the completion spec test.

### 14. Registry precedence audit

**Tier:** high

**Found:** The registry is consulted before a contract's spec, so a spec cannot rename a registered interface's arguments (`registry_first` in `TestExplainWithSpecNeverDecodes`). As more interfaces are added, two registries could match the same call.

**Done when:** Registration refuses two signatures with the same name, arity and types, with a test.

**Evidence:** The test failing on a deliberately duplicated signature.

### 15. Explain `create_contract` from an asset when the executable is not `stellar_asset`

**Tier:** high

**Found:** `explainCreateContract` renders "Create a contract from asset X, running Y" for a from-asset preimage with any executable other than `stellar_asset`. Whether the host accepts that combination has not been checked.

**Done when:** The host source for contract creation is read and cited, and the combination is either explained with that citation or refused as unexplainable.

**Evidence:** The cited host file and line; a snapshot case.

## Contract specs

### 16. Backlog item 2 from v0.1.0: read a contract's spec (done)

**Tier:** low

**Found:** Most real entries were `opaque` because their functions are outside SEP-41.

**Done when:** Done in c446471, 940b519, 803f1d2, 5d219be, 42fca25, b695a5e and b3c36b6: the `spec` package, `WithSpecs`, `--rpc`, and evidence that the opaque share of the recorded samples falls from 89.6% to 4.0% (testnet) and from 98.8% to 0.0% (public) while the decoded share does not change.

**Evidence:** Kept here so the history of the backlog is visible; close it on review.

### 17. Property-test substituted argument lists that fit the declared types

**Tier:** high

**Found:** A spec names a function's parameters, but an authorization records whatever the contract passed to `require_auth_for_args`, which "don't have to match the arguments of the contract invocation" (rs-soroban-sdk `soroban-sdk/src/address.rs:243`). The type check refuses lists that visibly differ (13 testnet actions did), but it cannot catch a substituted list that happens to fit the declared types. Today that limit lives only in a comment and a note.

**Done when:** A property or fuzz test generates, for real recorded specs, argument lists that are type-compatible but permuted or substituted, and asserts that every such rendering is `partial`, carries the substitution caveat, and is never `decoded`. The test's documentation states plainly what it cannot prove: that a name describes its value.

**Evidence:** The test's seed corpus from `testdata/live/*.specs.json`; its run output showing how many compatible substitutions exist per spec.

### 18. Detect `require_auth_for_args` from the wasm, and say so

**Tier:** high

**Found:** Whether a function calls `require_auth_for_args` with a different list is visible in its code, not its spec. The live sample shows real contracts doing it (`create_and_try_fill_with_fee` declares nine parameters and authorizes four values).

**Done when:** Research whether the wasm can be inspected, offline and bounded, to tell that a function authorizes a custom list. If it can, a named call on such a function carries a stronger note; it never becomes more certain.

**Evidence:** A written finding either way, with the wasm evidence for the 13 known cases.

### 19. Name the constructor arguments of `create_contract_v2`

**Tier:** high

**Found:** A V2 contract creation carries constructor arguments, rendered raw and `partial`. The wasm hash is in the entry, so its spec (the `__constructor` declaration) can be fetched by hash with `getLedgerEntries` on the ContractCode entry, without the contract existing yet. The recorded oracle spec shows such a declaration: `__constructor(config_manager: Address, publisher: Address)`.

**Done when:** `spec.RPC` can fetch a spec by wasm hash, and `WithSpecs` can name constructor arguments by hash; the action stays `partial`.

**Evidence:** Snapshot cases with a recorded constructor spec; a live example.

### 20. Name the fields of user-defined struct arguments

**Tier:** high

**Found:** Spec entries include `UdtStructV0` definitions, and `Matches` treats user-defined types as unchecked. A struct argument renders as a raw map.

**Done when:** When a spec defines the struct, a map argument whose keys match its fields exactly is rendered with the struct's name and field names; anything else stays raw. Confidence does not rise.

**Evidence:** Snapshot cases with a matching and a non-matching map; the spec entries recorded from a real contract.

### 21. Name enum and union cases

**Tier:** medium

**Found:** Spec entries include `UdtEnumV0` and `UdtUnionV0`. A `u32` enum value or a union's symbol-tagged vector renders raw.

**Done when:** Enum values and union cases that match a declared case render with the case name beside the raw value; confidence does not rise.

**Evidence:** Snapshot cases; the real spec they come from.

### 22. Name contract error codes

**Tier:** medium

**Found:** `ScvError` values of type contract render as `error(contract, N)`. A spec's `UdtErrorEnumV0` names those codes.

**Done when:** An error value whose code is declared renders its name beside the code.

**Evidence:** A snapshot case with a recorded error enum.

### 23. Deepen the type check for compound types

**Tier:** medium

**Found:** `spec.Function.Matches` checks vectors, maps and tuples one level deep and does not check element types.

**Done when:** Element types are checked recursively, bounded by the existing depth limit, with each rule documented.

**Evidence:** Table tests per compound type; a live measurement of whether any named call changes.

### 24. Cache specs by wasm hash across entries

**Tier:** medium

**Found:** `spec.RPC.Fetch` fetches each contract's instance and code for every entry. The testnet sample's 56 readable contracts share 44 distinct wasm.

**Done when:** A cache keyed by wasm hash (never by contract name) avoids refetching code, with a bound on its size.

**Evidence:** A test counting RPC requests with and without the cache.

### 25. Fetch the spec in force at the entry's ledger, or say that it cannot

**Tier:** high

**Found:** `TestLiveSpecs` fetches specs today for entries up to a week old; a contract upgraded in between may publish a different spec. The evidence document says so, but the library has no way to tell.

**Done when:** Research whether RPC exposes historical ledger entries. If not, `spec.RPC` reports the ledger it read at, and a named call's note can say which ledger's spec named it.

**Evidence:** A written finding with the RPC documentation cited.

### 26. Report which contracts had no spec, in the explanation

**Tier:** high

**Found:** `spec.RPC.Fetch` returns reasons for contracts it could not read, and the CLI prints them to stderr, but the explanation itself does not say that naming was attempted and failed.

**Done when:** Decide whether an `Unexplained` sentence should say a spec was unavailable; it must not change confidence, and must not make output depend on the network when `--rpc` is absent.

**Evidence:** Snapshot cases with and without a failed fetch.

### 27. Keep spec doc strings out of renderings, deliberately

**Tier:** low

**Found:** Spec functions and inputs carry `Doc` strings (up to 1024 bytes). They are contract-written prose and are not shown.

**Done when:** The decision is recorded in `spec`'s package doc with the reason, and a test fails if a doc string ever reaches a rendering.

**Evidence:** A snapshot case with a spec whose doc strings contain misleading text.

### 28. Fuzz the wasm section walker

**Tier:** medium

**Found:** `spec.Section` walks attacker-supplied wasm. It has table tests for twelve hostile shapes but no fuzzing.

**Done when:** A fuzz target over `spec.Section` and `spec.FromWasm`, seeded with the recorded oracle wasm and the hostile cases, runs in `ci-extra.yml`; seeds run in the ordinary suite.

**Evidence:** The seed corpus read by `go test -v` (case names in the output, section 11.5).

### 29. Fuzz the spec XDR parser

**Tier:** medium

**Found:** `spec.Parse` decodes attacker-supplied XDR with a depth and length limit.

**Done when:** A fuzz target over `spec.Parse` asserting no panic and no allocation beyond the input bound.

**Evidence:** As for the walker.

### 30. Record the contract's SDK version from `contractmetav0`

**Tier:** low

**Found:** The recorded oracle wasm has a `contractmetav0` custom section (96 bytes) next to its spec. It is not read.

**Done when:** Decide whether the evidence records it (for example, to correlate spec shapes with SDK versions). It must never affect a rendering.

**Evidence:** The decision recorded in `spec`'s doc.

### 31. Expose the parsed spec for inspection

**Tier:** low

**Found:** A developer checking why a call was not named has to write Go to see what the spec declares.

**Done when:** A CLI subcommand prints a contract's declared functions from `--rpc` or from a wasm file, wired through the dispatcher and completions.

**Evidence:** A test through `run()`.

### 32. Name arguments of calls on contracts deployed in the same entry

**Tier:** high

**Found:** An entry can create a contract and call it in the same tree. The new contract has no instance to fetch yet, but its wasm hash is in the creation action.

**Done when:** `spec.RPC.Fetch` fetches the spec of a contract created earlier in the entry by its wasm hash.

**Evidence:** A built snapshot case; a live example if one exists.

### 33. Bound the total bytes `--rpc` downloads per entry

**Tier:** medium

**Found:** Each contract's code is capped at `MaxWasmBytes` (256 KiB) and an entry at 64 contracts, so one entry can download up to 16 MiB.

**Done when:** A total cap per `Fetch` call, measured against real entries first (section 11.3), after which remaining contracts are skipped with a reason.

**Evidence:** The measured distribution of total bytes per entry in the live samples.

### 34. Test the spec reader against a second implementation

**Tier:** low

**Found:** Names were checked against the SDK's own XDR types but not against an independent spec reader.

**Done when:** A check in `ci-extra.yml` parses the recorded spec sections with another implementation (for example the JavaScript SDK's spec reader) and compares declared names and types.

**Evidence:** The check's output over all recorded sections.

## Interface families the spec reader now reaches

### 35. Price-oracle writes

**Tier:** high

**Found:** The spec reader now names these calls in the live samples (testnet: `set_price` 189 actions, `set_price_stable` 18, `write_prices` 15, `publish_round` 12, `push_price` 6, `submit_prices` 4; public: `write_prices` 8, `submit_prices` 3), but only names them: they stay `partial`.

**Done when:** Research first: find whether a published standard (a SEP, a CAP, or a specification the protocol's authors publish and version) defines these functions and what their arguments mean. Only a published, citable standard can support a registry entry (CONTRIBUTING.md, "Adding a registry entry"); a single protocol's own contract is not a standard, and the result may be that these calls stay `partial`. If a standard exists, register `set_price`, `set_prices`, `set_price_stable`, `write_prices`, `push_price`, `submit_prices`, `publish_round` from its committed text with a comparison test. If none exists, record that finding in `docs/` with the search that was made.

**Evidence:** The written finding with links; if a registry entry results, snapshot cases for the decoded, partial and impostor paths and a live check that runs against the host's own events.

### 36. AMM and router swaps

**Tier:** high

**Found:** The spec reader now names these calls in the live samples (public: `swap` 74 actions, `swap_chained` 11, `swap_split` 4, `swap_strict_receive` 1), but only names them: they stay `partial`.

**Done when:** Research first: find whether a published standard (a SEP, a CAP, or a specification the protocol's authors publish and version) defines these functions and what their arguments mean. Only a published, citable standard can support a registry entry (CONTRIBUTING.md, "Adding a registry entry"); a single protocol's own contract is not a standard, and the result may be that these calls stay `partial`. If a standard exists, register `swap`, `swap_chained`, `swap_split`, `swap_strict_receive` from its committed text with a comparison test. If none exists, record that finding in `docs/` with the search that was made.

**Evidence:** The written finding with links; if a registry entry results, snapshot cases for the decoded, partial and impostor paths and a live check that runs against the host's own events.

### 37. Lending markets

**Tier:** high

**Found:** The spec reader now names these calls in the live samples (testnet: `borrow` 1, `supply` 1, `repay_debt_with_collateral` 1; public: `liquidate_asset` 9, `withdraw` 1), but only names them: they stay `partial`.

**Done when:** Research first: find whether a published standard (a SEP, a CAP, or a specification the protocol's authors publish and version) defines these functions and what their arguments mean. Only a published, citable standard can support a registry entry (CONTRIBUTING.md, "Adding a registry entry"); a single protocol's own contract is not a standard, and the result may be that these calls stay `partial`. If a standard exists, register `borrow`, `supply`, `withdraw`, `repay_debt_with_collateral`, `liquidate_asset` from its committed text with a comparison test. If none exists, record that finding in `docs/` with the search that was made.

**Evidence:** The written finding with links; if a registry entry results, snapshot cases for the decoded, partial and impostor paths and a live check that runs against the host's own events.

### 38. Concentrated-liquidity positions

**Tier:** medium

**Found:** The spec reader now names these calls in the live samples (public: one action each), but only names them: they stay `partial`.

**Done when:** Research first: find whether a published standard (a SEP, a CAP, or a specification the protocol's authors publish and version) defines these functions and what their arguments mean. Only a published, citable standard can support a registry entry (CONTRIBUTING.md, "Adding a registry entry"); a single protocol's own contract is not a standard, and the result may be that these calls stay `partial`. If a standard exists, register `deposit_position`, `withdraw_position`, `claim_all_position_fees` from its committed text with a comparison test. If none exists, record that finding in `docs/` with the search that was made.

**Evidence:** The written finding with links; if a registry entry results, snapshot cases for the decoded, partial and impostor paths and a live check that runs against the host's own events.

### 39. Order books and vault orders

**Tier:** medium

**Found:** The spec reader now names these calls in the live samples (testnet: `create_order` 41, `place` 11, `cancel_vault_order` 7, `create_vault_order` 5, `cancel_order` 4), but only names them: they stay `partial`.

**Done when:** Research first: find whether a published standard (a SEP, a CAP, or a specification the protocol's authors publish and version) defines these functions and what their arguments mean. Only a published, citable standard can support a registry entry (CONTRIBUTING.md, "Adding a registry entry"); a single protocol's own contract is not a standard, and the result may be that these calls stay `partial`. If a standard exists, register `create_order`, `cancel_order`, `create_vault_order`, `cancel_vault_order`, `place` from its committed text with a comparison test. If none exists, record that finding in `docs/` with the search that was made.

**Evidence:** The written finding with links; if a registry entry results, snapshot cases for the decoded, partial and impostor paths and a live check that runs against the host's own events.

### 40. Batched and relayed execution

**Tier:** high

**Found:** The spec reader now names these calls in the live samples (testnet: `execute` 5, `transact` 5; public: `batch`, `exec`, `execute`, `relay` 1 each. Several of these carry other calls as arguments, which is where the substitution limit ("Property-test substituted argument lists that fit the declared types") bites hardest), but only names them: they stay `partial`.

**Done when:** Research first: find whether a published standard (a SEP, a CAP, or a specification the protocol's authors publish and version) defines these functions and what their arguments mean. Only a published, citable standard can support a registry entry (CONTRIBUTING.md, "Adding a registry entry"); a single protocol's own contract is not a standard, and the result may be that these calls stay `partial`. If a standard exists, register `batch`, `exec`, `execute`, `relay`, `transact`, `multicall_with_fee` from its committed text with a comparison test. If none exists, record that finding in `docs/` with the search that was made.

**Evidence:** The written finding with links; if a registry entry results, snapshot cases for the decoded, partial and impostor paths and a live check that runs against the host's own events.

### 41. Proof-of-work farming (KALE)

**Tier:** medium

**Found:** The spec reader now names these calls in the live samples (public: `plant` 259 actions, `batch_work` 73, `batch_plant` 7; KALE burns inside these batches are already decoded on the KALE SAC), but only names them: they stay `partial`.

**Done when:** Research first: find whether a published standard (a SEP, a CAP, or a specification the protocol's authors publish and version) defines these functions and what their arguments mean. Only a published, citable standard can support a registry entry (CONTRIBUTING.md, "Adding a registry entry"); a single protocol's own contract is not a standard, and the result may be that these calls stay `partial`. If a standard exists, register `plant`, `batch_plant`, `batch_work` from its committed text with a comparison test. If none exists, record that finding in `docs/` with the search that was made.

**Evidence:** The written finding with links; if a registry entry results, snapshot cases for the decoded, partial and impostor paths and a live check that runs against the host's own events.

### 42. Payments and instalments

**Tier:** medium

**Found:** The spec reader now names these calls in the live samples (testnet: `process_outgoing_transaction` 8, `process_incoming_transaction` 3, `credit_account` 3, `pay` 2, `pay_installment` 1), but only names them: they stay `partial`.

**Done when:** Research first: find whether a published standard (a SEP, a CAP, or a specification the protocol's authors publish and version) defines these functions and what their arguments mean. Only a published, citable standard can support a registry entry (CONTRIBUTING.md, "Adding a registry entry"); a single protocol's own contract is not a standard, and the result may be that these calls stay `partial`. If a standard exists, register `pay`, `pay_installment`, `credit_account`, `process_incoming_transaction`, `process_outgoing_transaction` from its committed text with a comparison test. If none exists, record that finding in `docs/` with the search that was made.

**Evidence:** The written finding with links; if a registry entry results, snapshot cases for the decoded, partial and impostor paths and a live check that runs against the host's own events.

### 43. Claims, disputes and escrow

**Tier:** medium

**Found:** The spec reader now names these calls in the live samples (testnet: one action each; public: `claim` 1), but only names them: they stay `partial`.

**Done when:** Research first: find whether a published standard (a SEP, a CAP, or a specification the protocol's authors publish and version) defines these functions and what their arguments mean. Only a published, citable standard can support a registry entry (CONTRIBUTING.md, "Adding a registry entry"); a single protocol's own contract is not a standard, and the result may be that these calls stay `partial`. If a standard exists, register `claim`, `approve_claim`, `cancel_claim`, `dispute`, `resolve`, `seal` from its committed text with a comparison test. If none exists, record that finding in `docs/` with the search that was made.

**Evidence:** The written finding with links; if a registry entry results, snapshot cases for the decoded, partial and impostor paths and a live check that runs against the host's own events.

### 44. Reporting and rate publication

**Tier:** medium

**Found:** The spec reader now names these calls in the live samples (testnet: `report` 12, `rollup` 12, `update_indices` 16; public: `publish_statements` 15, `publish_rate` 2), but only names them: they stay `partial`.

**Done when:** Research first: find whether a published standard (a SEP, a CAP, or a specification the protocol's authors publish and version) defines these functions and what their arguments mean. Only a published, citable standard can support a registry entry (CONTRIBUTING.md, "Adding a registry entry"); a single protocol's own contract is not a standard, and the result may be that these calls stay `partial`. If a standard exists, register `report`, `publish_statements`, `publish_rate`, `rollup`, `update_indices` from its committed text with a comparison test. If none exists, record that finding in `docs/` with the search that was made.

**Evidence:** The written finding with links; if a registry entry results, snapshot cases for the decoded, partial and impostor paths and a live check that runs against the host's own events.

### 45. Replacement and batch updates

**Tier:** medium

**Found:** The spec reader now names these calls in the live samples (testnet: `replace_batch` 26, `replace` 12, `set_multiple_values` 8), but only names them: they stay `partial`.

**Done when:** Research first: find whether a published standard (a SEP, a CAP, or a specification the protocol's authors publish and version) defines these functions and what their arguments mean. Only a published, citable standard can support a registry entry (CONTRIBUTING.md, "Adding a registry entry"); a single protocol's own contract is not a standard, and the result may be that these calls stay `partial`. If a standard exists, register `replace`, `replace_batch`, `set_multiple_values` from its committed text with a comparison test. If none exists, record that finding in `docs/` with the search that was made.

**Evidence:** The written finding with links; if a registry entry results, snapshot cases for the decoded, partial and impostor paths and a live check that runs against the host's own events.

### 46. Contract upgrades and migrations deserve a warning, derived from the bytes only

**Tier:** high

**Found:** The recorded oracle spec declares `upgrade(new_wasm_hash, operator)`, `propose_upgrade`, `cancel_upgrade` and `migrate`. An authorization to replace a contract's code is among the most consequential a signer can give, yet a spec-named `upgrade` call reads like any other.

**Done when:** Research whether the host exposes code replacement in a way visible in an authorization entry (for example as a host function). If it does, decode that path. If only a contract's own function name suggests it, record why the tool must not claim it: a function called `upgrade` is not evidence of an upgrade.

**Evidence:** The cited host or CAP text; snapshot cases.

### 47. Administrative and circuit-breaker functions

**Tier:** medium

**Found:** testnet: `reset_all_circuit_breakers` 2, `set_manual_override` 2, `set_publisher` (declared in the oracle spec). These change who controls a contract or its safety limits.

**Done when:** Research first: find whether a published standard (a SEP, a CAP, or a specification the protocol's authors publish and version) defines these functions and what their arguments mean. Only a published, citable standard can support a registry entry (CONTRIBUTING.md, "Adding a registry entry"); a single protocol's own contract is not a standard, and the result may be that these calls stay `partial`. Whatever the finding, these stay named-only unless a standard defines them.

**Evidence:** The written finding.

### 48. Classify the remaining one-off functions

**Tier:** low

**Found:** The testnet sample names about twenty functions seen once or twice (`classify_flights`, `multiply`, `set_score`, `apply_reserve_attestation`, `refresh`, `post`, …).

**Done when:** A short table in `docs/` groups them by apparent domain from their spec names only, clearly labelled as a guess about names, to guide which families to research next. It feeds no rendering.

**Evidence:** The table, regenerated from the live records rather than written by hand.

### 49. Measure how often named calls carry another call as an argument

**Tier:** medium

**Found:** Batch and relay functions (`batch`, `relay`, `execute`, `multicall_with_fee`) take calls as arguments. The tool renders those arguments raw.

**Done when:** A measurement over both live records of named calls whose arguments contain contract addresses and symbols in a call-like shape, reported in the evidence document.

**Evidence:** The measurement, generated from the records.

## Evidence and live checks

### 50. Check decoded actions from failed transactions in-repo

**Tier:** high

**Found:** In the public sample, 48 decoded actions sat in failed transactions, which emit no events, so the live check reports `no-events`. For CP4 they were cross-checked outside the repository with `@stellar/stellar-sdk` 17.1.0 (329 actions, 0 mismatches). That check is not reproducible from the repo. This was item 5 of the first backlog, and `not-executed` (which cannot tell an unexecuted call from a wrong function name) makes it more important.

**Done when:** A reproducible second-implementation check runs in `ci-extra.yml` and reports on every decoded action, with no code shared with the explanation path.

**Evidence:** Its output for both records; a deliberately corrupted amount and label caught, as at CP4.

### 51. Check SAC deployments against ledger state

**Tier:** medium

**Found:** A decoded "Deploy the Stellar Asset Contract for …" is reported `not-checked` by the live check, which handles wasm contracts created from an address only. This was item 7 of the first backlog.

**Done when:** The check derives the SAC ID for the rendered asset and confirms from the transaction's meta that a `stellar_asset` instance was created there.

**Evidence:** A regression case with a real deployment, if sampling finds one.

### 52. Record a futurenet run

**Tier:** medium

**Found:** Live evidence covers testnet and the public network. The SDK defines `network.FutureNetworkPassphrase` ("Test SDF Future Network ; October 2022").

**Done when:** A `TestLiveFuturenet` run, if a futurenet RPC is available, recorded alongside the others.

**Evidence:** The record and the regenerated evidence document; if no RPC is available, a note saying so.

### 53. Refresh the recorded runs on a schedule

**Tier:** medium

**Found:** `testdata/live/*.json` are single samples from 2026-09-28. `ci-extra.yml` re-samples on every push to `main` but records nothing.

**Done when:** A documented cadence for re-recording, and a changelog of evidence runs, so the README's figures state their date.

**Evidence:** The first refreshed run, with the diff in the evidence document explained.

### 54. Report the confidence distribution by credential type

**Tier:** medium

**Found:** 456 of 480 testnet entries are source-account, so the headline rates mostly describe source-account entries.

**Done when:** The evidence document breaks the distribution down by credential type.

**Evidence:** Generated from the records.

### 55. Report sample diversity next to the rates

**Tier:** low

**Found:** One testnet contract accounts for 85 of 480 entries. The evidence document lists the ten most frequent root functions but not how concentrated the sample is.

**Done when:** The document reports the share of entries from the top contract and top function.

**Evidence:** Generated from the records.

### 56. Grow the samples to a size that supports the percentages quoted

**Tier:** high

**Found:** Each run has about 480 entries. The README quotes rates to one decimal place.

**Done when:** Work out, and document, the sample size needed for the quoted precision, then record runs of that size or quote coarser figures.

**Evidence:** The calculation and the runs.

### 57. Record which spec-named actions the substitution caveat is known to apply to

**Tier:** medium

**Found:** 13 testnet actions were refused because their authorized list differed from the spec. Nothing records which contracts do this.

**Done when:** The evidence document lists contracts observed authorizing custom argument lists.

**Evidence:** Generated from the records and the recorded specs.

### 58. Make the live check's categories a documented enum

**Tier:** low

**Found:** `event-match`, `ledger-match`, `no-events`, `not-executed`, `not-checked`, `no-matching-event`, `asset-mismatch`, `ledger-mismatch` and `check-error` are strings scattered through `live_test.go`.

**Done when:** One typed list with a description of each, used by the checks and by the evidence document.

**Evidence:** The evidence document's explanation generated from that list.

### 59. Check approvals that were authorized but not executed

**Tier:** medium

**Found:** `not-executed` is now decided per party, but only for the first party. An approval's spender is not considered.

**Done when:** Decide and document which parties identify an executed call for each kind, with a regression case for each.

**Evidence:** Cases in `TestLiveCheckRegressions`.

### 60. Alert on advisory live failures

**Tier:** medium

**Found:** Three advisory live failures during the spec-reader work were found by reading `gh run list`, not by a notification.

**Done when:** A documented way the maintainer learns of an advisory failure (a scheduled summary, an issue opened by the workflow, or a README badge), chosen with the maintainer.

**Evidence:** The chosen mechanism working once.

### 61. Record RPC versions in live runs

**Tier:** low

**Found:** The recorded runs name the RPC URL but not the server version, which could explain differences between runs.

**Done when:** Live records include the result of `getVersionInfo` (or the equivalent RPC method, checked against the RPC documentation).

**Evidence:** A record with the field.

### 62. Measure the time `--rpc` adds

**Tier:** medium

**Found:** `spec.RPC.Fetch` makes two requests per contract. Its cost per entry has not been measured.

**Done when:** A live measurement of `--rpc` latency per entry on both networks, recorded in the evidence document and used to set `rpcTimeout` (section 11.3).

**Evidence:** The measurement.

### 63. Keep the evidence document's check description in step with the checks

**Tier:** low

**Found:** The document's prose about checks was edited by hand three times as checks changed.

**Done when:** The prose is generated from the enum in the item above, so it cannot lag.

**Evidence:** The generated text.

## Fixtures and snapshot cases

### 64. A real decoded `transfer_from` fixture

**Tier:** medium

**Found:** No snapshot case covers a decoded `transfer_from` from real traffic.

**Done when:** A real entry, with its transaction hash, added through the generator.

**Evidence:** The snapshot, explained in the commit.

### 65. A real decoded `burn_from` fixture

**Tier:** medium

**Found:** As above, for `burn_from`.

**Done when:** As above.

**Evidence:** As above.

### 66. An alphanumeric-12 asset label fixture

**Tier:** low

**Found:** Every issued-asset label in the snapshots uses a code of four characters or fewer.

**Done when:** A built case with a 12-character code on a derived SAC.

**Evidence:** The snapshot.

### 67. An asset code with characters outside A-Z, a-z and 0-9

**Tier:** low

**Found:** `canonicalAssetLabel` refuses such a code, but no snapshot shows the result.

**Done when:** A built case whose SAC is derived from such a code, rendered without a label.

**Evidence:** The snapshot.

### 68. Numeric asset codes

**Tier:** low

**Found:** The public sample decoded a burn on the SAC of an asset whose code is `1` (`1:GB4P3…`). Labels such as `1:G…` are easy to misread.

**Done when:** A snapshot case pins that rendering, and the README mentions it.

**Evidence:** The snapshot.

### 69. A delegate entry with a spec-named call

**Tier:** medium

**Found:** No snapshot combines delegates (CAP-71-01) with spec naming.

**Done when:** A built case with nested delegates over a spec-named call.

**Evidence:** The snapshot.

### 70. Every ScVal type as a spec-named argument

**Tier:** low

**Found:** `TestRenderScValEveryType` covers each type, but not inside a named call.

**Done when:** A built spec declaring one input of each concrete type, and a call using them.

**Evidence:** The snapshot.

### 71. A V2 creation with constructor arguments from real traffic

**Tier:** medium

**Found:** The constructor-argument path is covered by built cases only.

**Done when:** A real entry with its transaction hash.

**Evidence:** The snapshot.

### 72. An entry exactly at the decode depth limit

**Tier:** low

**Found:** `TestDecodeDepthLimit` measures the boundary (29 nested vectors under MaxDepth 64) but no snapshot input sits on it.

**Done when:** A built input at the boundary and one past it, rendered through the CLI.

**Evidence:** The snapshots.

### 73. An entry exactly at the node limit

**Tier:** low

**Found:** `built_hundred_arguments` is far below `DefaultMaxNodes` (1024).

**Done when:** Built inputs at and one past the node limit.

**Evidence:** The snapshots, one of them a refusal.

### 74. A real public-network `plant` entry with its recorded spec

**Tier:** medium

**Found:** `plant` is the most frequent public root function (259 of 481 entries), but only a testnet oracle call has a spec snapshot.

**Done when:** A real entry and its recorded spec, through the generator.

**Evidence:** The snapshot.

### 75. A real public-network `swap` entry with its recorded spec

**Tier:** medium

**Found:** As above, for `swap` (74 entries).

**Done when:** As above.

**Evidence:** As above.

### 76. A spec that declares the same function twice

**Tier:** low

**Found:** `TestParseDuplicateDropped` covers the parser, but no snapshot shows the resulting rendering.

**Done when:** A built case whose call stays `opaque`.

**Evidence:** The snapshot.

### 77. A spec whose function takes an Option

**Tier:** low

**Found:** `Matches` accepts void for an Option; no snapshot shows it.

**Done when:** Built cases with the option present and absent.

**Evidence:** The snapshots.

### 78. Fixtures from soroauth-go's later vectors, once re-pinned

**Tier:** medium

**Found:** soroauth-go's main has vectors v0.1.0 lacks, including source-account ones (brief section 7). A re-pin has conditions (section 0).

**Done when:** When a re-pin happens, the generator copies the new vectors and the built source-account case is kept or retired with a reason.

**Evidence:** The generator run on the new version.

### 79. A signed entry with a real signature in every arm

**Tier:** low

**Found:** The copied vectors include signed forms, but no built case pairs a signature with a spec-named call.

**Done when:** A built case, signed with a test key by soroauth-go in a test helper (never a real key).

**Evidence:** The snapshot.

### 80. Name every snapshot case in one table

**Tier:** low

**Found:** There are 35 cases across `testdata/entries`. Their purposes are only in each file's `source` field.

**Done when:** A generated table in `testdata/README.md` lists each case and its source.

**Evidence:** Generated by `cmd/gensnapshots`, checked by the drift job.

### 81. Record the live entries that produced each finding as fixtures

**Tier:** medium

**Found:** The three regression transactions (`c2426626…`, `cc265b25…`, `55ea6c0a…`) are fetched by hash in a tagged test and will leave RPC retention.

**Done when:** Their envelopes and metas are recorded under `testdata/` so the regression test runs offline.

**Evidence:** The regression test passing with no network.

## Renderers and output

### 82. Machine-readable reason codes for everything in `Unexplained`

**Tier:** high

**Found:** `Unexplained` is a list of English sentences. A wallet that wants to show its own warning, or translate one, has to parse prose.

**Done when:** Each reason has a stable code and structured parameters (contract, function, argument index) alongside the sentence, in `Explanation` and the JSON rendering. The sentence stays, generated from the code.

**Evidence:** A wire-format change with a CHANGELOG entry; snapshot changes explained; a test that every sentence the library can produce has a code.

### 83. Publish a JSON Schema for the wire format

**Tier:** high

**Found:** The JSON field names are a wire format (brief section 6.7), but there is no schema a wallet can validate against.

**Done when:** A committed JSON Schema, and a test that validates every committed JSON snapshot against it.

**Evidence:** The test; a snapshot deliberately altered to break the schema, caught.

### 84. Add a format version to the JSON

**Tier:** medium

**Found:** A wallet cannot tell which version of the wire format it received.

**Done when:** A top-level version field, with the policy for bumping it in CHANGELOG.md.

**Evidence:** Snapshot changes explained; CHANGELOG entry.

### 85. Say in the JSON where each name and label came from

**Tier:** high

**Found:** A field named `arg[0]:caller` came from a contract's spec; an `asset` field came from a derivation. The JSON does not say which.

**Done when:** Each field carries its provenance (`bytes`, `derivation`, `registry`, `spec`), so a wallet can style spec names differently.

**Evidence:** Wire-format change; snapshots; CHANGELOG.

### 86. A one-line summary for constrained displays

**Tier:** medium

**Found:** A hardware wallet or notification has room for one line. The text renderer's first line names the credential, not the action.

**Done when:** A `render.Line` (or similar) that states the root action and the confidence in one line, never dropping the confidence.

**Evidence:** Snapshot cases in a third format.

### 87. A Markdown renderer

**Tier:** medium

**Found:** Reviewers paste renderings into pull requests and chats, where the text renderer's alignment breaks.

**Done when:** `render.Markdown` with a snapshot for every case, drift-checked like the others.

**Evidence:** The new snapshot format.

### 88. Show full addresses with visual grouping

**Tier:** low

**Found:** Renderings show full 56-character strkeys. Truncating them invites look-alike addresses, but they are hard to compare by eye.

**Done when:** An optional grouped form (for example in fours) that never drops characters, in the text renderer only.

**Evidence:** Snapshot cases.

### 89. Group thousands in scaled amounts, locale-free

**Tier:** medium

**Found:** `100.0000000` is readable; `17014118346046923173168730371588.4105727` is not.

**Done when:** An option to group integer digits with a fixed separator, deterministic and never locale-dependent (section 5).

**Evidence:** Snapshot cases; a test that output does not change with `LANG`.

### 90. Avoid repeating the action tree for each delegate in text

**Tier:** low

**Found:** The text renderer already lists delegates without repeating actions; the JSON repeats them. This was item 10 of the first backlog.

**Done when:** A decision, with the wallet use case in mind, on whether the JSON references the entry's actions instead. Any change is a breaking wire-format change.

**Evidence:** The decision recorded; CHANGELOG if it changes.

### 91. Render timepoints and durations with their unit, not a date

**Tier:** medium

**Found:** `timepoint(1700000000)` and `duration(60)` are shown raw. Converting to a date needs a time zone (section 5 forbids reading one from the environment).

**Done when:** Decide whether to show `timepoint(1700000000, seconds since the Unix epoch)` and the like. Any date form must be UTC and fixed-format.

**Evidence:** Snapshot cases.

### 92. Colour-free by default, colour only on request

**Tier:** low

**Found:** The text renderer has no colour. If colour is ever added, confidence must not be carried by colour alone (see Accessibility).

**Done when:** A documented rule, and a test, that any colour option is off unless asked for and never the only signal.

**Evidence:** The test.

### 93. Stable ordering rules, written down

**Tier:** medium

**Found:** Renderings are deterministic (map entries in stored order, notes in tree order), but the ordering rules are only in code comments.

**Done when:** A section in the package docs listing every ordering rule, each pointing at the test that enforces it.

**Evidence:** The doc and the tests it cites.

### 94. Escape rules, written down

**Tier:** low

**Found:** Strings are Go-quoted, symbols outside [A-Za-z0-9_] are quoted, spec names outside that set are withheld. These rules are spread across `scval.go` and `explain.go`.

**Done when:** One documented escape policy with a test per rule.

**Evidence:** The tests.

### 95. Render byte strings that are addresses or hashes more usefully

**Tier:** medium

**Found:** `bytes[32](…)` is shown as hex. Some are wasm hashes or public keys, but the bytes alone do not say which.

**Done when:** Decide what, if anything, can be derived. A 32-byte value must never be labelled as a key or hash on its length alone.

**Evidence:** A written decision; snapshot cases if anything changes.

### 96. Show the argument count in named calls' field list consistently

**Tier:** low

**Found:** Named calls and opaque calls both carry an `arguments` field; registry calls do not.

**Done when:** A consistent rule, applied everywhere, with snapshot changes explained.

**Evidence:** Snapshots.

### 97. An HTML fragment renderer for wallet webviews

**Tier:** high

**Found:** Wallets that embed a webview need escaped HTML. Rendering the text output into HTML is easy to get wrong.

**Done when:** `render.HTML` producing an escaped fragment with no scripts, styles or external resources, and a test that contract-supplied strings cannot inject markup.

**Evidence:** Snapshot cases, including hostile strings.

## Command-line interface

### 98. Read `--entry` from a file

**Tier:** medium

**Found:** `--entry` takes base64 or `-` for stdin. Reviewers often have the entry in a file.

**Done when:** `--entry @path` (or a separate flag) reads a file with the same limits and trimming as stdin, wired into usage and completions.

**Evidence:** Tests through `run()`; the completion spec test.

### 99. Explain every entry in a transaction envelope

**Tier:** high

**Found:** The CLI takes one authorization entry. Users usually have a whole transaction. soroauth-go's `InspectEnvelope` and `EnvelopeEntries` are post-v0.1.0 (brief section 0).

**Done when:** An envelope input that decodes a transaction with this tool's own limits and explains each entry. Whether to use soroauth-go's functions depends on a re-pin, which has conditions.

**Evidence:** Snapshot cases with real envelopes; the section-0 conditions checked before any re-pin.

### 100. Explain several entries, one per line

**Tier:** medium

**Found:** Piping a list of entries requires a shell loop.

**Done when:** A mode reading one entry per line from stdin and printing one explanation per entry, with a combined exit code documented.

**Evidence:** Tests through a real pipe.

### 101. `--version`

**Tier:** low

**Found:** The CLI cannot report its version.

**Done when:** `--version` prints the module version from build info, wired through usage and completions.

**Evidence:** A test through `run()`.

### 102. Make `--rpc`'s timeout configurable

**Tier:** low

**Found:** `rpcTimeout` is fixed at 30 seconds.

**Done when:** A flag, once the latency measurement in "Measure the time `--rpc` adds" exists to set a sensible default.

**Evidence:** The measurement; tests.

### 103. Cache fetched specs between CLI runs

**Tier:** medium

**Found:** `--rpc` refetches every contract every run.

**Done when:** An opt-in on-disk cache keyed by wasm hash, never by contract name or label.

**Evidence:** Tests counting requests across runs.

### 104. Print which contracts `--rpc` named

**Tier:** low

**Found:** With `--rpc`, stderr lists contracts whose spec could not be read, but not which were named.

**Done when:** A `--verbose` or similar that lists the contracts and wasm hashes used.

**Evidence:** Tests.

### 105. Generate a man page

**Tier:** low

**Found:** The CLI has usage text and shell completions but no man page.

**Done when:** A generated man page from the same source as the usage text, with a test that they agree.

**Evidence:** The test.

### 106. A policy mode that fails on specific kinds, not only on confidence

**Tier:** high

**Found:** `--strict` fails unless everything is decoded. A policy engine may want to fail on, for example, any approval to an unidentified spender.

**Done when:** Design and document a narrow, declarative policy input. It must never pass something `--strict` would fail because of a spec.

**Evidence:** Tests for each rule.

### 107. Test the CLI on Windows line endings end to end

**Tier:** low

**Found:** CRLF input is tested through `run()`, but not through a real Windows shell.

**Done when:** A CI job on a Windows runner runs the pipe test, if the maintainer wants Windows support.

**Evidence:** The job's output.

### 108. Completions for `--rpc` values

**Tier:** low

**Found:** `--network` completes `testnet` and `public`; `--rpc` completes nothing.

**Done when:** `--rpc` offers the RPC URLs the live tests use, clearly as suggestions.

**Evidence:** The completion behaviour tests for bash and fish.

### 109. Exit codes as a documented, tested table

**Tier:** medium

**Found:** Exit codes 0 to 3 are documented in `main.go` and the README.

**Done when:** One table, with a test that every documented code is produced by some test case.

**Evidence:** The test.

## Robustness, limits and security

### 110. Fuzz `Explain`

**Tier:** high

**Found:** `Explain` walks attacker-controlled entries. It has table tests and limit tests, but no fuzzing. This was item 3 of the first backlog.

**Done when:** A fuzz target over decoded entries asserting: no panic, no result beyond the limits, confidence equal to the floor of its nodes, and a non-decoded explanation always has reasons. Seeds from `testdata/entries` run in the ordinary suite; fuzzing runs in `ci-extra.yml` (section 11.7).

**Evidence:** Seed case names in `go test -v` output (section 11.5).

### 111. Fuzz the CLI's decoder

**Tier:** high

**Found:** `decodeEntry` is the first code to see hostile input.

**Done when:** A fuzz target over `decodeEntry` asserting that the length check always precedes decoding and no input panics.

**Evidence:** As above.

### 112. Property test the confidence floor over random trees

**Tier:** high

**Found:** The floor rule is tested on hand-built trees.

**Done when:** A property test builds random invocation trees with random known and unknown calls and checks the floor and the `Unexplained` invariant everywhere.

**Evidence:** The property test and its seed.

### 113. Measure memory use at the limits

**Tier:** medium

**Found:** The node and depth limits bound work, but the memory used by an explanation at the limits has not been measured.

**Done when:** A benchmark at `DefaultMaxNodes` and `DefaultMaxDepth` reporting allocations, with the figures recorded.

**Evidence:** The benchmark output.

### 114. Benchmark rendering

**Tier:** medium

**Found:** No benchmarks exist.

**Done when:** Benchmarks for `Explain`, `render.Text` and `render.JSON` over the snapshot corpus.

**Evidence:** The output, recorded with its date.

### 115. Treat RPC responses as hostile, with tests

**Tier:** medium

**Found:** `spec.RPC` caps response size and checks the code hash, but an RPC can still return a valid-looking instance for the wrong contract.

**Done when:** The fetched instance's contract address is checked against the one requested, with a test.

**Evidence:** A fake RPC returning another contract's instance, caught.

### 116. Refuse redirects to other hosts in `--rpc`

**Tier:** low

**Found:** `spec.RPC` uses the default HTTP client, which follows redirects.

**Done when:** Decide and test whether a redirect to another host is followed.

**Evidence:** A test with a redirecting server.

### 117. Run `govulncheck` in the advisory workflow

**Tier:** medium

**Found:** No vulnerability scanning runs.

**Done when:** `govulncheck ./...` in `ci-extra.yml`.

**Evidence:** Its first output.

### 118. Run `staticcheck`

**Tier:** low

**Found:** Only `go vet` runs.

**Done when:** `staticcheck` in CI, after its findings on the current tree are fixed or recorded.

**Evidence:** Its output.

### 119. A threat model document

**Tier:** medium

**Found:** The honesty boundary and the impostor guard are described in the README and brief, but there is no single threat model.

**Done when:** `docs/THREAT_MODEL.md` listing what an attacker controls (the entry, contract specs, symbols, strings, RPC responses) and which code defends each.

**Evidence:** Each defence linked to its test.

### 120. Check that no rendering contains a control character

**Tier:** low

**Found:** Strings and symbols are escaped, but no test scans all output.

**Done when:** A test over every snapshot asserting no byte below 0x20 other than newline.

**Evidence:** The test.

### 121. Bound the stderr output of `--rpc` failures

**Tier:** medium

**Found:** An entry calling 64 unreadable contracts prints 64 lines to stderr.

**Done when:** A cap on reported failures with a count of the rest.

**Evidence:** A test.

## Internationalization

### 122. A message catalogue for all user-facing text

**Tier:** high

**Found:** Summaries, notes and CLI messages are English strings built in code. Translating them means changing code.

**Done when:** Every sentence comes from a catalogue keyed by the reason codes above, with English as the reference. Rendering stays deterministic: the language is an explicit option, never read from the environment (section 5).

**Evidence:** Snapshots in English unchanged; a test that every catalogue key is used.

### 123. A pseudo-locale to find untranslated strings

**Tier:** medium

**Found:** Without a catalogue, there is no way to see which strings are still hard-coded.

**Done when:** A pseudo-locale that marks every catalogued string, with a test that no English text appears unmarked.

**Evidence:** The test.

### 124. Keep values untranslated

**Tier:** medium

**Found:** Addresses, amounts, symbols and hex must never be localised; a translated summary must still carry every field value (the Summary/Fields agreement test).

**Done when:** `checkSummaryAgrees` runs for every locale.

**Evidence:** The test in the pseudo-locale.

### 125. Document the plain-English style of the notes

**Tier:** low

**Found:** The notes follow a style (plain sentences, no jargon beyond protocol names), but it is not written down for translators.

**Done when:** A short style guide in `docs/` for writing and translating notes.

**Evidence:** The guide.

### 126. Right-to-left safety in text output

**Tier:** high

**Found:** A translated summary in a right-to-left script could reorder addresses and amounts when displayed.

**Done when:** Research and document how values are isolated from surrounding text in bidirectional output, and test it.

**Evidence:** The written finding and tests.

### 127. Locale-independent number formatting, tested

**Tier:** low

**Found:** Amounts are formatted with integer arithmetic and no locale, but no test proves the output ignores `LANG` and `LC_ALL`.

**Done when:** A test running the CLI under different locale environment variables with identical output.

**Evidence:** The test.

### 128. A first translation, as a proof of the catalogue

**Tier:** medium

**Found:** A catalogue with only English is untested as a catalogue.

**Done when:** One complete translation by a fluent contributor, reviewed by a second.

**Evidence:** Snapshot cases in that language.

### 129. Translate the CLI usage text

**Tier:** low

**Found:** Usage text is a Go string.

**Done when:** Usage comes from the catalogue too, with the synopsis (flags) left untranslated.

**Evidence:** Tests.

### 130. Keep translations from weakening the caveats

**Tier:** low

**Found:** A translation could soften "not interpreted" or "may not describe the value".

**Done when:** A review checklist for translators, requiring each caveat to keep its meaning.

**Evidence:** The checklist.

## Accessibility

### 131. Audit the text rendering with a screen reader

**Tier:** high

**Found:** The text renderer aligns fields with spaces and uses bracketed tags like `[decoded]`. How it reads aloud has not been checked.

**Done when:** A documented audit with at least one screen reader, and changes (or a separate accessible format) where it reads poorly. Confidence must always be spoken.

**Evidence:** The audit notes; snapshot changes explained.

### 132. Never convey confidence by position or symbol alone

**Tier:** medium

**Found:** Confidence is written as a word in brackets today. Any future visual rendering could drop the word.

**Done when:** A test over every renderer that the confidence word appears on every node.

**Evidence:** The test.

### 133. An accessible rendering without column alignment

**Tier:** medium

**Found:** Aligned `=` columns help sighted readers and add noise for screen readers.

**Done when:** A renderer option or format without alignment padding.

**Evidence:** Snapshot cases.

### 134. Read long addresses in chunks

**Tier:** low

**Found:** A 56-character strkey read aloud is hard to follow.

**Done when:** Research how screen readers handle long identifiers and document a recommended wallet presentation.

**Evidence:** The written finding.

### 135. Plain-language level of the notes

**Tier:** medium

**Found:** The notes mention protocol terms (`require_auth_for_args`, Stellar Asset Contract) that a general user may not know.

**Done when:** Each note has a plain-language form, or a glossary link, tested for reading level with a named tool.

**Evidence:** The tool's scores before and after.

### 136. Accessible HTML output

**Tier:** low

**Found:** If the HTML renderer above is built, it needs semantic structure (lists, headings) rather than styled divs.

**Done when:** The HTML renderer's output passes an automated accessibility checker.

**Evidence:** The checker's output.

### 137. Keyboard-only use of the completions

**Tier:** low

**Found:** Completions help keyboard users; zsh's behaviour is untested (only its syntax).

**Done when:** A zsh behaviour test like the bash and fish ones. This was item 4 of the first backlog.

**Evidence:** The test in CI.

### 138. Contrast and colour guidance for wallets

**Tier:** low

**Found:** Wallets choose how to show confidence. There is no guidance.

**Done when:** A short guide recommending text plus colour, never colour alone, for each confidence level.

**Evidence:** The guide.

## Documentation

### 139. A wallet integration guide

**Tier:** medium

**Found:** Wallets are the main consumers, but the only integration material is the README's example.

**Done when:** `docs/WALLETS.md`: decoding with limits, fetching specs, showing `Unexplained`, handling each confidence level, and what never to infer.

**Evidence:** Every code sample compiled as an Example.

### 140. "Why is my entry opaque?"

**Tier:** medium

**Found:** The most common question a user will have. The answer (outside the known interfaces; try `--rpc`; SACs have no spec) is spread across documents.

**Done when:** A short FAQ entry, linked from the README.

**Evidence:** The FAQ.

### 141. A glossary

**Tier:** low

**Found:** Terms such as credential arm, delegate, SAC, spec and derivation are used without definition.

**Done when:** `docs/GLOSSARY.md`, linked from the README.

**Evidence:** The glossary.

### 142. Architecture decision records for the honesty rules

**Tier:** medium

**Found:** Decisions such as `CODE:ISSUER` labels, `native` rather than `XLM`, delegates carrying actions, and specs never decoding are recorded only in commit messages.

**Done when:** One ADR per decision with its evidence and the commit that made it.

**Evidence:** The ADRs.

### 143. Examples for every exported function

**Tier:** low

**Found:** `Explain` and `spec.RPC.Fetch` have examples; `SACContractID`, `AssetLabel`, `Floor` and the renderers do not.

**Done when:** A compiled example for each, with output where it is offline.

**Evidence:** `go test` running them.

### 144. Document the evidence method for contributors

**Tier:** low

**Found:** How the live checks work is in `live_test.go` and the evidence document, not in one guide.

**Done when:** A section in CONTRIBUTING.md on reading and extending the evidence.

**Evidence:** The section.

### 145. A comparison with other decoders, with a dated search

**Tier:** medium

**Found:** The brief lists projects building their own decoders, found by a search dated 2026-09-28. The README makes no comparison, because such claims rot.

**Done when:** A dated comparison in `docs/`, re-checked by search, that cites each project and states what it does and does not do.

**Evidence:** The search results, dated.

### 146. A changelog policy for the wire format

**Tier:** low

**Found:** CHANGELOG.md says renaming a JSON field is breaking, but not how deprecations work.

**Done when:** A written deprecation policy.

**Evidence:** The policy.

### 147. Document `--rpc` endpoints

**Tier:** low

**Found:** The README says `--rpc` takes a Soroban RPC URL but lists none.

**Done when:** The endpoints the live tests use, with a note that any Soroban RPC works and that the URL receives the contracts being examined.

**Evidence:** The README section.

### 148. Explain the confidence levels with the live examples

**Tier:** low

**Found:** The README's examples are built cases. Real examples are in the evidence document.

**Done when:** Link each level in the README to a real example in the evidence document.

**Evidence:** The links, checked by the README test.

## Project infrastructure

### 149. Measure test coverage before setting any floor

**Tier:** medium

**Found:** No coverage measurement exists. The sibling project documented one floor while enforcing another (section 11.3). This was item 9 of the first backlog.

**Done when:** Coverage measured and reported with its date; any CI floor set below the measurement, recorded next to it.

**Evidence:** The measurement.

### 150. Reproducible release builds

**Tier:** high

**Found:** `v0.1.0` has no release binaries.

**Done when:** A release workflow building the CLI reproducibly, with checksums, triggered by a tag.

**Evidence:** Two builds producing identical checksums.

### 151. Dependency update policy

**Tier:** low

**Found:** The dependencies are pinned (soroauth-go v0.1.0, the SDK v0.7.3), and section 0 governs re-pinning soroauth-go, but not the SDK.

**Done when:** A written policy for SDK updates, including re-running the live evidence.

**Evidence:** The policy.

### 152. A software bill of materials

**Tier:** low

**Found:** No SBOM is published.

**Done when:** An SBOM generated for each release.

**Evidence:** The SBOM for the next release.

### 153. Pin GitHub Actions by commit

**Tier:** low

**Found:** Workflows use `actions/checkout@v7` and `actions/setup-go@v7` tags.

**Done when:** Actions pinned by commit SHA, with a note on how they are updated.

**Evidence:** The workflows.

### 154. Keep the live records small

**Tier:** low

**Found:** `testdata/live` is about 4 MB after dropping the JSON renderings.

**Done when:** Measure what else can be dropped or compressed without losing reproducibility, and do it.

**Evidence:** The size before and after, and the evidence test still passing.

### 155. Make the backlog machine-checkable

**Tier:** medium

**Found:** This document states per-tier counts, checked by a test, but items are not linked to issues or commits.

**Done when:** Once items become issues, each links to its issue, and a test checks no item is duplicated.

**Evidence:** The test.

### 156. Label snapshot changes in pull requests automatically

**Tier:** low

**Found:** Section 7 requires a pull request that changes a snapshot to explain it. Nothing flags such pull requests.

**Done when:** A check (advisory) that comments when `testdata/snapshots/` changes, reminding the author of the rule.

**Evidence:** The check on a test pull request.
