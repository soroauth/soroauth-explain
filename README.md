# soroauth-explain

soroauth-explain is a Go library and CLI that reads a Soroban authorization entry and gives an account of
what it authorizes. Where it cannot tell, it says so, and does not guess.

It is the sibling of [soroauth-go](https://github.com/soroauth/soroauth-go), which makes sure the right
bytes get signed. This project describes what those bytes authorize. It never signs anything and holds no
keys.

Status: **v0.1.0, unaudited.**

## Confidence: the one thing to understand first

Every explanation, every action in it, and every field of every action carries one of three levels:

| Level | Meaning | Example |
|---|---|---|
| `decoded` | Every element came from the bytes or from a derivation the library checks offline. | `Transfer 100.0000000 native from GARC…FRVX to GAZT…HCM6` |
| `partial` | The call shape is known, but some arguments are not interpreted. | `Transfer 1000000000 units of the token at CCW6…MI75 from … to …` |
| `opaque` | The function is unknown to this library. | `Call do_thing on CBCE…J5HZ with 3 arguments` |

Confidence does not average; it takes the floor. One `opaque` action anywhere in the call tree keeps the
whole explanation from being `decoded`. Anything that is not `decoded` comes with plain sentences, under
"Not determined" (`Unexplained` in the API), saying what could not be established and why.

## How often it can explain real entries

Measured on real transactions, and recorded in [docs/EVIDENCE.md](docs/EVIDENCE.md):

<!-- evidence: summary-table -->
| Run | Entries | Decoded, default | Decoded, with event assets | Opaque, with event assets | Opaque, with assets and specs | Decoded actions checked | Checks passed |
|---|---:|---:|---:|---:|---:|---:|---:|
| testnet | 480 | 7 (1.5%) | 35 (7.3%) | 430 (89.6%) | 19 (4.0%) | 170 | 170 |
| public | 481 | 0 (0.0%) | 6 (1.2%) | 475 (98.8%) | 0 (0.0%) | 159 | 111 |

On testnet the tool decoded **1.5%** of 480 real entries with no extra input, and **7.3%** when the assets
involved were supplied as candidates. On the public network it decoded **0.0%** and **1.2%** of 481. Most
entries are `opaque`.

Supplying each contract's own published spec changes the picture, and what it changes matters. On the same
testnet sample the opaque share falls from **89.6%** to **4.0%**, and on the public network from **98.8%** to
**0.0%**. Those entries become `partial`, not `decoded`: the spec names each argument (`caller`, `symbol`,
`price`), which says what the contract calls it, not what the contract does with it. A spec names a
function's parameters, and a contract can authorize a different list of arguments, so a name may not even
describe the value beside it, and the rendering says so. The decoded share does not move, and a test fails
if a spec ever makes anything `decoded`.

That is the tool working, not failing. An `opaque` entry is a call to a contract function outside the one
interface this library knows, the SEP-41 token interface, such as an oracle's `set_price` or a game's
`plant`. The bytes decoded fine; their meaning is not something the tool can establish, so it shows the
call and its raw arguments and says it does not know.

Every action it marked `decoded` in these runs is listed in the evidence document and checked against the
host's own events or ledger state. The last two columns show where that check could not run: on the
public network, 48 decoded actions sat inside transactions that failed, which emit no events, so the
host could not confirm them. The samples are shown as they came, dominated on testnet by price oracles.

## Install

```sh
go install github.com/soroauth/soroauth-explain/cmd/soroauth-explain@v0.1.0
```

As a library:

```sh
go get github.com/soroauth/soroauth-explain@v0.1.0
```

## Worked examples

Each block below is a committed snapshot, rendered by the CLI from an input in `testdata/entries/`. A test
fails if this README and the snapshot disagree.

### decoded

A SEP-41 `transfer` on the testnet native asset's Stellar Asset Contract:

```sh
soroauth-explain --entry "$ENTRY" --network testnet
```

<!-- snapshot: built_native_sac_transfer.txt -->
```text
Authorization entry: address credentials for GAIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCF6M [decoded]
  nonce:              7
  valid until ledger: 1000
  address-bound:      no
  signed:             no

Authorizes:
  [decoded] Transfer 100.0000000 native from GARCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCFRVX to GAZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTHCM6
      contract = CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC [decoded]
      function = transfer [decoded]
      asset    = native [decoded]
      from     = GARCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCFRVX [decoded]
      to       = GAZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTHCM6 [decoded]
      amount   = 100.0000000 (raw 1000000000) [decoded]
```

Every element is derived. The contract is the native asset's contract because its ID is the one derived
from the native asset and the testnet passphrase. The amount is scaled by 7 decimal places because every
Stellar Asset Contract uses 7, a constant in the host, and the raw integer from the bytes is shown beside
it.

### partial

A `transfer` on the public network's USDC contract, explained without naming USDC as a candidate:

```sh
soroauth-explain --entry "$ENTRY" --network public
```

<!-- snapshot: built_usdc_transfer_without_asset.txt -->
```text
Authorization entry: address credentials for GAIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCF6M [partial]
  nonce:              7
  valid until ledger: 1000
  address-bound:      no
  signed:             no

Authorizes:
  [partial] Transfer 1000000000 units of the token at CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75 from GARCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCFRVX to GAZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTHCM6
      contract = CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75 [decoded]
      function = transfer [decoded]
      from     = GARCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCFRVX [decoded]
      to       = GAZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTHCM6 [decoded]
      amount   = 1000000000 [partial]

Not determined:
  - The token at CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75 is not identified on this network, so its asset and decimal places are unknown; amounts are shown as raw integers in its smallest unit.
  - The call matches the SEP-41 transfer signature, but a matching signature does not show that the contract at CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75 behaves as SEP-41 describes.
```

The call matches the SEP-41 `transfer` signature, so the parties are named. Nothing identifies the token,
so the amount stays a raw integer. Adding `--asset USDC:GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN`
lets the tool derive that asset's contract ID, find it matches, and render the same entry `decoded` as
`Transfer 100.0000000 USDC:GA5Z…KZVN`.

### opaque

A call to a function the library does not know:

<!-- snapshot: built_unknown_function.txt -->
```text
Authorization entry: address credentials for GAIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCF6M [opaque]
  nonce:              7
  valid until ledger: 1000
  address-bound:      no
  signed:             no

Authorizes:
  [opaque] Call do_thing on CBCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEJ5HZ with 3 arguments
      contract  = CBCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEJ5HZ [decoded]
      function  = do_thing [decoded]
      arguments = 3 [decoded]
      arg[0]    = GAZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTHCM6 [decoded]
      arg[1]    = i128(42) [decoded]
      arg[2]    = bytes[2](cafe) [decoded]

Not determined:
  - The function do_thing on CBCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEJ5HZ is not interpreted by this library; its 3 arguments are shown as raw values.
```

## The impostor guard

The failure this project exists to avoid is telling someone "100 XLM" about a contract that is not the
native asset. A contract can implement `transfer` with exactly the token signature and report any name and
symbol it likes.

So a contract is labelled with an asset **only** when its contract ID is the Stellar Asset Contract ID
derived from that asset and the network passphrase. Nothing the contract says about itself is read, no
name is looked up, and a matching function signature earns nothing. Without `--network` (`WithNetwork`),
no contract is labelled at all.

Labels always carry the issuer: `native`, or `CODE:ISSUER`. A bare code is ambiguous. The public network
has more than one USDC issuer with a real Stellar Asset Contract, and the testnet run in
[docs/EVIDENCE.md](docs/EVIDENCE.md) decoded transfers and approvals of USDC from four different issuers,
each told apart by its label.

This is the test case for the guard: a real Stellar Asset Contract for an *issued* asset coded `XLM`,
whose `symbol()` reports `XLM` and whose `transfer` has the token signature. Its ID does not derive from
the native asset, so it renders as its address and nothing more:

<!-- snapshot: built_impostor_transfer.txt -->
```text
Authorization entry: address credentials for GAIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCF6M [partial]
  nonce:              7
  valid until ledger: 1000
  address-bound:      no
  signed:             no

Authorizes:
  [partial] Transfer 1000000000 units of the token at CCQQMNELP664QCF26DMH555EPD7IRGIO6J6Z77ZDLVFE5PY2IDY3XOIY from GARCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCFRVX to GAZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTHCM6
      contract = CCQQMNELP664QCF26DMH555EPD7IRGIO6J6Z77ZDLVFE5PY2IDY3XOIY [decoded]
      function = transfer [decoded]
      from     = GARCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCFRVX [decoded]
      to       = GAZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTHCM6 [decoded]
      amount   = 1000000000 [partial]

Not determined:
  - The token at CCQQMNELP664QCF26DMH555EPD7IRGIO6J6Z77ZDLVFE5PY2IDY3XOIY is not identified on this network, so its asset and decimal places are unknown; amounts are shown as raw integers in its smallest unit.
  - The call matches the SEP-41 transfer signature, but a matching signature does not show that the contract at CCQQMNELP664QCF26DMH555EPD7IRGIO6J6Z77ZDLVFE5PY2IDY3XOIY behaves as SEP-41 describes.
```

Amounts follow the same rule. They are scaled only for a derived Stellar Asset Contract. Any other token
contract may use any number of decimal places, and a wrong scale is the same class of error as a wrong
name, so its amounts are shown as raw integers.

## Using the library

<!-- example: ExampleExplain -->
```go
// An entry someone sent you is hostile input: bound its size before any
// base64 or XDR work, and its nesting while decoding.
if len(entryXDR) > base64.StdEncoding.EncodedLen(1<<20) {
	fmt.Println("entry too large")
	return
}
var entry xdr.SorobanAuthorizationEntry
if err := xdr.SafeUnmarshalBase64WithOptions(entryXDR, &entry, xdr.DecodeOptions{MaxDepth: 64}); err != nil {
	fmt.Println("decode:", err)
	return
}

exp, err := explain.Explain(entry, explain.WithNetwork(network.TestNetworkPassphrase))
if err != nil {
	fmt.Println("explain:", err)
	return
}
fmt.Println(exp.Confidence)
fmt.Println(exp.Actions[0].Summary)
for _, reason := range exp.Unexplained {
	fmt.Println("not determined:", reason)
}
```

Candidate assets for labelling are passed with `explain.WithAssets(...)`; each is still derived and
compared.

To name the arguments of calls the library does not interpret, fetch the called contracts' own specs and
pass them in. Fetching is the only networked step and takes a context; `Explain` itself stays offline:

```go
specs, _ := spec.RPC{URL: "https://soroban-testnet.stellar.org"}.Fetch(ctx, entry)
exp, err := explain.Explain(entry, explain.WithNetwork(network.TestNetworkPassphrase), explain.WithSpecs(specs))
```

A contract whose spec cannot be fetched or read is left out, and its calls render as they would with no
spec. A spec-named call is at most `partial`. `render.Text` and `render.JSON` (package `render`) produce the two output formats. The JSON field
names are a wire format: renaming one is a breaking change.

Limits, all exported with their reasons: `explain.DefaultMaxDepth` (32) and `explain.DefaultMaxNodes`
(1024) bound the walk over an entry, and a limit hit is an error, never a truncated explanation.

## The CLI

```text
soroauth-explain --entry <base64|-> [--network testnet|public|<passphrase>]
                 [--asset CODE:ISSUER] [--rpc <url>] [--json] [--strict]
soroauth-explain completions --shell bash|zsh|fish
```

- `--entry -` reads the entry from stdin and trims surrounding whitespace, so it composes with other tools'
  output.
- `--asset` is repeatable. `native` is always a candidate. A value that is not `CODE:ISSUER` is a usage
  error.
- `--rpc <url>` is the only thing that makes the tool use the network, and only when given. It fetches
  the published spec of each contract the entry calls from that Soroban RPC and names their arguments.
  Named calls stay `partial`, so `--strict` still fails on them. If a fetch fails, the entry is explained
  as it would be without `--rpc`, with a note on stderr.
- `--strict` exits 3 unless the explanation is `decoded`, for use in a policy check. The rendering is
  still printed.
- Results go to stdout and diagnostics to stderr. On failure stdout is empty. Exit codes: 0 success,
  1 the entry could not be read, decoded or explained, 2 usage error, 3 `--strict` and not decoded.
- Input is decoded with this tool's own limits: at most 1 MiB decoded, checked before any base64 or XDR
  work, and a nesting depth of 64.

## Differences from soroauth-go

| | soroauth-go | soroauth-explain |
|---|---|---|
| Answers | Are the right bytes signed? | What do these bytes authorize? |
| Signs | Yes | Never. No keys, no seeds, no signer. |
| Reports | Structure: credential arm, addresses, nonce, expiration, delegate tree, signed nodes | Meaning, where it can be derived, with a confidence on every node |
| Depends on | The Stellar Go SDK | soroauth-go (for `Inspect` and address formatting) and the SDK |

soroauth-explain takes the structure of an entry from soroauth-go's `Inspect` rather than decoding it a
second way, so the two cannot disagree about the same entry. It depends on soroauth-go v0.1.0.

## What it does not do

It explains what an entry authorizes. It does not predict what a transaction will do or cost, simulate
it, sign it, or look anything up about a contract. An explanation is `decoded`, `partial` or `opaque`,
and nothing more.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). In short: every rendering is pinned by a committed snapshot that
is never edited by hand, and CI regenerates the snapshots and fails on any difference. A change that makes
the tool claim more, such as a new registry entry, shows up as a snapshot diff that has to be explained.
Report security problems privately, as described in [SECURITY.md](SECURITY.md): a wrong `decoded`
rendering is treated as critical.

## License

Apache-2.0. See [LICENSE](LICENSE).
