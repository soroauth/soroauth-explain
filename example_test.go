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
