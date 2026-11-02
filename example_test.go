package explain_test

import (
	"encoding/base64"
	"fmt"

	explain "github.com/soroauth/soroauth-explain"
	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// entryXDR is testdata/entries/built_native_sac_transfer.json: a transfer on
// the testnet native Stellar Asset Contract.
const entryXDR = "AAAAAQAAAAAAAAAAEREREREREREREREREREREREREREREREREREREREREREAAAAAAAAABwAAA+gAAAABAAAAAAAAAAHXkotywnA8z+r365/0701QSlWouXn8m0UOoshCtNHOYQAAAAh0cmFuc2ZlcgAAAAMAAAASAAAAAAAAAAAiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIgAAABIAAAAAAAAAADMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzAAAACgAAAAAAAAAAAAAAADuaygAAAAAA"

func ExampleExplain() {
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
	// Output:
	// decoded
	// Transfer 100.0000000 native from GARCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCEIRCFRVX to GAZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTGMZTHCM6
}

// envelopeXDR is testdata/entries/envelope_public_fee_bump_transfer_with_asset.json:
// public-network transaction d118280cea8b794f0558e078b0485d8a63c81b9b9262da4d74781418fcb90d71,
// a fee-bumped USDC transfer.
const envelopeXDR = "AAAABQAAAABrWwE/DB0Lbjtq+HvGOe1joNZsigvfyVumG+i225al0wAAAAAAA95BAAAAAgAAAABepUgOyonkyo+1V3dncKAxDpUT2zn+nvGK8I2U2gW6IwACV6EDpTlKAAAALwAAAAEAAAAAAAAAAAAAAABq57+ZAAAAAAAAAAEAAAAAAAAAGAAAAAAAAAABre/OWa7lKWj3YGHUlMJSW3Vln6QpamX0me8p5WR35JYAAAAIdHJhbnNmZXIAAAADAAAAEgAAAAAAAAAAXqVIDsqJ5MqPtVd3Z3CgMQ6VE9s5/p7xivCNlNoFuiMAAAASAAAAAfZGcv5PJWaDGC/UPGz77vXh0M0JZkMAx9JWYz1GwtTdAAAACgAAAAAAAAAAAAAAAAKIf6AAAAABAAAAAAAAAAAAAAABre/OWa7lKWj3YGHUlMJSW3Vln6QpamX0me8p5WR35JYAAAAIdHJhbnNmZXIAAAADAAAAEgAAAAAAAAAAXqVIDsqJ5MqPtVd3Z3CgMQ6VE9s5/p7xivCNlNoFuiMAAAASAAAAAfZGcv5PJWaDGC/UPGz77vXh0M0JZkMAx9JWYz1GwtTdAAAACgAAAAAAAAAAAAAAAAKIf6AAAAAAAAAAAQAAAAAAAAABAAAABgAAAAGt785ZruUpaPdgYdSUwlJbdWWfpClqZfSZ7ynlZHfklgAAABQAAAABAAAAAgAAAAEAAAAAXqVIDsqJ5MqPtVd3Z3CgMQ6VE9s5/p7xivCNlNoFuiMAAAABVVNEQwAAAAA7mRE4Dv6Yi6CokA6xz+RPNm99vpRr7QdyQPf2JN8VxQAAAAYAAAABre/OWa7lKWj3YGHUlMJSW3Vln6QpamX0me8p5WR35JYAAAAQAAAAAQAAAAIAAAAPAAAAB0JhbGFuY2UAAAAAEgAAAAH2RnL+TyVmgxgv1Dxs++714dDNCWZDAMfSVmM9RsLU3QAAAAEABHvnAAAAoAAAAYAAAAAAAADRAQAAAAFVvw/GAAAAQCUEwGnZRY1jESAeOo3Noc7f3odT2UcQg64XadS6KqhHZBjsdmu/DZjc/ZRojUztlfE/2cG1ob8KQ7u1BvpuogUAAAAAAAAAAduWpdMAAABA7Ke2sYxG4GRySuI/R1gl74rxyQMSU5YKWbBIXluiWEtSP8ZXpGtvVdCVjHQYsSDWT7uKQqpjzL5hUtm5XBRyBg=="

func ExampleExplainEnvelope() {
	if len(envelopeXDR) > base64.StdEncoding.EncodedLen(1<<20) {
		fmt.Println("envelope too large")
		return
	}
	var env xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64WithOptions(envelopeXDR, &env, xdr.DecodeOptions{MaxDepth: 64}); err != nil {
		fmt.Println("decode:", err)
		return
	}

	// USDC is named only because this candidate derives to the contract the
	// transaction calls, on this network.
	usdc := xdr.MustNewCreditAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	exp, err := explain.ExplainEnvelope(env,
		explain.WithNetwork(network.PublicNetworkPassphrase), explain.WithAssets(usdc))
	if err != nil {
		fmt.Println("explain:", err)
		return
	}
	fmt.Println(exp.Confidence)
	for _, op := range exp.Operations {
		// Only an InvokeHostFunction operation has an invocation; any other
		// operation is listed, opaque, with a reason in Unexplained.
		if op.Invocation != nil {
			fmt.Println(op.Type, "invokes:", op.Invocation.Summary)
		}
		fmt.Println(len(op.Entries), "authorization entry")
	}
	for _, reason := range exp.Unexplained {
		fmt.Println("not determined:", reason)
	}
	// Output:
	// decoded
	// InvokeHostFunction invokes: Transfer 4.2500000 USDC:GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN from GBPKKSAOZKE6JSUPWVLXOZ3QUAYQ5FIT3M475HXRRLYI3FG2AW5CHRCL to CD3EM4X6J4SWNAYYF7KDY3H35326DUGNBFTEGAGH2JLGGPKGYLKN22OZ
	// 1 authorization entry
}
