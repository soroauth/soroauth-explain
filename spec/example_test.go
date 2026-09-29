package spec_test

import (
	"context"
	"fmt"

	explain "github.com/soroauth/soroauth-explain"
	"github.com/soroauth/soroauth-explain/spec"
	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// ExampleRPC_Fetch is compiled but not run (it has no Output), because it
// reaches the network.
func ExampleRPC_Fetch() {
	ctx := context.Background()
	var entry xdr.SorobanAuthorizationEntry // decoded with limits, as in ExampleExplain

	specs, _ := spec.RPC{URL: "https://soroban-testnet.stellar.org"}.Fetch(ctx, entry)
	exp, err := explain.Explain(entry, explain.WithNetwork(network.TestNetworkPassphrase), explain.WithSpecs(specs))
	if err != nil {
		fmt.Println("explain:", err)
		return
	}
	fmt.Println(exp.Confidence)
}
