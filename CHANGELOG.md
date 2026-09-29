# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

The JSON rendering (`render.JSON`) is a wire format for wallets. Renaming or removing one of its fields is
a breaking change and is listed here as one.

## [Unreleased]

### Added

- Package `spec` reads a contract's own interface spec from its wasm (`FromWasm`, `Parse`, `Section`),
  bounded by `MaxWasmBytes`, and `spec.RPC` fetches it over Soroban RPC with a context (`Wasm`, `Spec`,
  `Fetch`, `Contracts`). `Fetch` never fails as a whole.
- `explain.WithSpecs` names the arguments of calls the library does not otherwise interpret. A named call
  is at most `partial`: a spec names a function's parameters, not what the contract does with them, and a
  contract can authorize different arguments from its parameters. On the recorded testnet sample the
  opaque share falls from 89.6% to 4.0%, and on the public sample from 98.8% to 0.0%; the decoded share
  does not change.

### Fixed

- `WithAssets` kept a reference to the caller's slice until `Explain` ran; it now copies its arguments
  when called.

## [0.1.0] - 2026-09-29

First release. Unaudited.

### Added

**Explanations** (package `explain`)

- `Explain` turns a `xdr.SorobanAuthorizationEntry` into an `Explanation`: who authorizes (structure
  taken from soroauth-go's `Inspect`), what is authorized (an `Action` tree, one node per invocation), and
  `Unexplained`, plain sentences saying what could not be determined.
- Confidence levels `ConfidenceDecoded`, `ConfidencePartial` and `ConfidenceOpaque`, with `Floor`: an
  explanation is only as certain as its least certain node, and an unknown value floors to opaque.
- Source-account entries render their invocation tree like any other entry; the credential arm changes
  who authorizes, not what.
- Delegates (CAP-71-01) carry the entry's actions, confidence and reasons, since they sign the same
  payload.
- Options: `WithNetwork` (required for any asset label), `WithAssets` (candidate assets beyond native),
  `WithMaxDepth`, `WithMaxNodes`. Limits `DefaultMaxDepth` (32) and `DefaultMaxNodes` (1024); a limit hit
  is an error, never a truncated explanation.
- Sentinel errors `ErrDepthLimit`, `ErrNodeLimit`, `ErrUnsupportedCredentials`, `ErrEmptyArm`,
  `ErrUnknownNetwork`, `ErrNoSnapshot`.
- Every `ScVal` type is rendered with an explicit case; unknown or unrenderable values are opaque.
  128- and 256-bit integers are reassembled exactly. Contract-supplied strings and symbols are quoted so
  control characters cannot reach a terminal.

**The impostor guard**

- `SACContractID` derives a Stellar Asset Contract ID from an asset and a network passphrase, using the
  SDK's `xdr.Asset.ContractID`. It was checked against real SAC instances fetched from testnet and the
  public network (`testdata/sac`).
- `AssetLabel` labels a contract only when it is the derived SAC ID of a candidate asset on that network.
  Labels are `native` or `CODE:ISSUER`, never a bare code.

**Known interfaces** (package `interfaces`)

- The SEP-41 token interface (v0.5.2): `approve`, `transfer`, `transfer_from`, `burn`, `burn_from`,
  matched by name, arity and argument types, and checked against the SEP's committed text.
- A match on a derived SAC is `decoded`, with amounts scaled by the host's fixed SAC decimal count (7). On
  any other contract it is `partial`, with raw amounts and the reasons stated.

**Renderers** (package `render`)

- `render.Text` for people, with confidence on every node.
- `render.JSON`, the stable wire format: sorted keys, exact integers, trailing newline.

**CLI** (`cmd/soroauth-explain`)

- `soroauth-explain --entry <base64|-> [--network testnet|public|<passphrase>] [--asset CODE:ISSUER]
  [--json] [--strict]`, and `soroauth-explain completions --shell bash|zsh|fish`.
- Input is decoded with this tool's own limits: 1 MiB, checked before any base64 or XDR work, and nesting
  depth 64. `--entry -` trims surrounding whitespace so it composes with other tools' output.
- `--strict` exits 3 unless the explanation is decoded. Stdout carries only results.

**Correctness gate and evidence**

- 31 snapshot cases (soroauth-go v0.1.0's golden vectors plus built cases, including an impostor token, a
  depth-limit tree, an unknown function and a 100-argument call), rendered in both formats and compared
  byte for byte. CI regenerates them and fails on drift.
- Live tests (build tag `live`) explain real testnet and public-network entries and check every decoded
  action against the host's own events or ledger state. `docs/EVIDENCE.md` records the runs: 1.5% of
  real testnet entries decoded by default (7.3% with candidate assets), and 0.0% on the public network
  (1.2%).

### Dependencies

- `github.com/soroauth/soroauth-go` v0.1.0
- `github.com/stellar/go-stellar-sdk` v0.7.3

[0.1.0]: https://github.com/soroauth/soroauth-explain/releases/tag/v0.1.0
