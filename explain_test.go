package explain

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	soroauth "github.com/soroauth/soroauth-go"
	"github.com/stellar/go-stellar-sdk/xdr"
)

var (
	delegateKeyA = [32]byte{0xa0}
	delegateKeyB = [32]byte{0xb0}
	otherKey     = [32]byte{0x03}
)

func i128Val(n int64) xdr.ScVal {
	return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &xdr.Int128Parts{Hi: 0, Lo: xdr.Uint64(n)}}
}

func addrVal(a xdr.ScAddress) xdr.ScVal {
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &a}
}

func callInvocation(contract xdr.ScAddress, fn string, args []xdr.ScVal, subs ...xdr.SorobanAuthorizedInvocation) xdr.SorobanAuthorizedInvocation {
	return xdr.SorobanAuthorizedInvocation{
		Function: xdr.SorobanAuthorizedFunction{
			Type: xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn,
			ContractFn: &xdr.InvokeContractArgs{
				ContractAddress: contract,
				FunctionName:    xdr.ScSymbol(fn),
				Args:            args,
			},
		},
		SubInvocations: subs,
	}
}

func createInvocation(deployer xdr.ScAddress, args []xdr.ScVal) xdr.SorobanAuthorizedInvocation {
	pre := xdr.ContractIdPreimage{
		Type:        xdr.ContractIdPreimageTypeContractIdPreimageFromAddress,
		FromAddress: &xdr.ContractIdPreimageFromAddress{Address: deployer, Salt: xdr.Uint256{0x5a}},
	}
	exe := xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableWasm, WasmHash: &xdr.Hash{0x77}}
	if args == nil {
		return xdr.SorobanAuthorizedInvocation{Function: xdr.SorobanAuthorizedFunction{
			Type:                 xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeCreateContractHostFn,
			CreateContractHostFn: &xdr.CreateContractArgs{ContractIdPreimage: pre, Executable: exe},
		}}
	}
	return xdr.SorobanAuthorizedInvocation{Function: xdr.SorobanAuthorizedFunction{
		Type:                   xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeCreateContractV2HostFn,
		CreateContractV2HostFn: &xdr.CreateContractArgsV2{ContractIdPreimage: pre, Executable: exe, ConstructorArgs: args},
	}}
}

func deploySACInvocation(asset xdr.Asset) xdr.SorobanAuthorizedInvocation {
	return xdr.SorobanAuthorizedInvocation{Function: xdr.SorobanAuthorizedFunction{
		Type: xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeCreateContractHostFn,
		CreateContractHostFn: &xdr.CreateContractArgs{
			ContractIdPreimage: xdr.ContractIdPreimage{Type: xdr.ContractIdPreimageTypeContractIdPreimageFromAsset, FromAsset: &asset},
			Executable:         xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableStellarAsset},
		},
	}}
}

func addressEntry(root xdr.SorobanAuthorizedInvocation) xdr.SorobanAuthorizationEntry {
	return xdr.SorobanAuthorizationEntry{
		Credentials: xdr.SorobanCredentials{
			Type: xdr.SorobanCredentialsTypeSorobanCredentialsAddress,
			Address: &xdr.SorobanAddressCredentials{
				Address:                   accountAddress(testAccountKey),
				Nonce:                     42,
				SignatureExpirationLedger: 1234567,
				Signature:                 xdr.ScVal{Type: xdr.ScValTypeScvVoid},
			},
		},
		RootInvocation: root,
	}
}

func signedVal() xdr.ScVal {
	return vecVal(u32Val(1))
}

func delegatesEntry(root xdr.SorobanAuthorizedInvocation) xdr.SorobanAuthorizationEntry {
	return xdr.SorobanAuthorizationEntry{
		Credentials: xdr.SorobanCredentials{
			Type: xdr.SorobanCredentialsTypeSorobanCredentialsAddressWithDelegates,
			AddressWithDelegates: &xdr.SorobanAddressCredentialsWithDelegates{
				AddressCredentials: xdr.SorobanAddressCredentials{
					Address:                   contractAddress(testContractKey),
					Nonce:                     -1,
					SignatureExpirationLedger: 99,
					Signature:                 xdr.ScVal{Type: xdr.ScValTypeScvVoid},
				},
				Delegates: []xdr.SorobanDelegateSignature{
					{Address: accountAddress(delegateKeyA), Signature: signedVal(), NestedDelegates: []xdr.SorobanDelegateSignature{
						{Address: accountAddress(delegateKeyB), Signature: xdr.ScVal{Type: xdr.ScValTypeScvVoid}},
					}},
					{Address: accountAddress(delegateKeyB), Signature: signedVal()},
				},
			},
		},
		RootInvocation: root,
	}
}

func sourceAccountEntry(root xdr.SorobanAuthorizedInvocation) xdr.SorobanAuthorizationEntry {
	return xdr.SorobanAuthorizationEntry{
		Credentials:    xdr.SorobanCredentials{Type: xdr.SorobanCredentialsTypeSorobanCredentialsSourceAccount},
		RootInvocation: root,
	}
}

// checkInvariants asserts the rules every Explanation obeys, at every level:
// confidence is the floor of its actions and delegates, anything not decoded
// says why, and no Summary disagrees with its Fields.
func checkInvariants(t testing.TB, exp Explanation) {
	t.Helper()
	if exp.Confidence != ConfidenceDecoded && len(exp.Unexplained) == 0 {
		t.Errorf("%s explanation of %q has an empty Unexplained", exp.Confidence, exp.Subject)
	}
	want := ConfidenceDecoded
	for _, a := range exp.Actions {
		want = Floor(want, a.Confidence)
		checkAction(t, a)
	}
	for _, d := range exp.Delegates {
		want = Floor(want, d.Confidence)
		checkInvariants(t, d)
	}
	if exp.Confidence != want {
		t.Errorf("confidence of %q = %s, floor of its nodes is %s", exp.Subject, exp.Confidence, want)
	}
}

func checkAction(t testing.TB, a Action) {
	t.Helper()
	checkSummaryAgrees(t, a)
	floor := a.Confidence
	for _, f := range a.Fields {
		if Floor(floor, f.Confidence) != floor {
			t.Errorf("action %q is %s but field %s is %s", a.Summary, a.Confidence, f.Name, f.Confidence)
		}
	}
	for _, s := range a.Sub {
		if Floor(a.Confidence, s.Confidence) != a.Confidence {
			t.Errorf("action %q is %s but a sub-action is %s", a.Summary, a.Confidence, s.Confidence)
		}
		checkAction(t, s)
	}
}

func explainChecked(t testing.TB, entry xdr.SorobanAuthorizationEntry, opts ...Option) Explanation {
	t.Helper()
	before, err := entry.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	exp, err := Explain(entry, opts...)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	after, err := entry.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("Explain modified the caller's entry")
	}
	checkInvariants(t, exp)
	return exp
}

// TestExplainSourceAccount: the arm changes who authorizes, not what. The
// invocation tree is explained and floors the confidence like any other
// entry's; an opaque call makes a source-account entry opaque.
func TestExplainSourceAccount(t *testing.T) {
	opaque := explainChecked(t, sourceAccountEntry(callInvocation(contractAddress(testContractKey), "do_thing", nil)))
	if opaque.CredentialType != soroauth.CredentialTypeSourceAccount || opaque.Subject != "" || opaque.Signed || opaque.Nonce != 0 {
		t.Fatalf("structure = %+v", opaque)
	}
	if len(opaque.Actions) != 1 || opaque.Actions[0].Function != "do_thing" {
		t.Fatalf("the authorized call is not shown: %+v", opaque.Actions)
	}
	if opaque.Confidence != ConfidenceOpaque || len(opaque.Unexplained) == 0 {
		t.Fatalf("confidence = %s, unexplained = %q; want opaque with reasons", opaque.Confidence, opaque.Unexplained)
	}

	decoded := explainChecked(t, sourceAccountEntry(createInvocation(accountAddress(testAccountKey), nil)))
	if decoded.Confidence != ConfidenceDecoded || len(decoded.Actions) != 1 || decoded.Actions[0].Kind != ActionCreateContract {
		t.Fatalf("got %+v", decoded)
	}
}

func TestExplainOpaqueCall(t *testing.T) {
	contract := contractAddress(testContractKey)
	entry := addressEntry(callInvocation(contract, "do_thing",
		[]xdr.ScVal{addrVal(accountAddress(otherKey)), i128Val(1000000000), symVal("x")}))
	exp := explainChecked(t, entry)

	if exp.Confidence != ConfidenceOpaque {
		t.Fatalf("confidence = %s, want opaque", exp.Confidence)
	}
	if exp.Nonce != 42 || exp.ValidUntilLedger != 1234567 || exp.Signed || exp.CredentialType != "address" {
		t.Fatalf("structure = %+v", exp)
	}
	a := exp.Actions[0]
	if a.Kind != ActionInvokeContract || a.Function != "do_thing" || a.Contract != exp.Actions[0].Fields[0].Value {
		t.Fatalf("action = %+v", a)
	}
	if !strings.Contains(a.Summary, "Call do_thing on C") || !strings.HasSuffix(a.Summary, "with 3 arguments") {
		t.Fatalf("summary = %q", a.Summary)
	}
	if got := a.Fields[4].Value; got != "i128(1000000000)" {
		t.Fatalf("arg[1] = %q, want the raw integer", got)
	}
}

func TestExplainUnplainFunctionName(t *testing.T) {
	exp := explainChecked(t, addressEntry(callInvocation(contractAddress(testContractKey), "a\x1bb", nil)))
	if a := exp.Actions[0]; a.Function != `"a\x1bb"` || strings.Contains(a.Summary, "\x1b") {
		t.Fatalf("function name not quoted: %+v", a)
	}
}

func TestExplainCreateContract(t *testing.T) {
	deployer := accountAddress(testAccountKey)
	tests := []struct {
		name string
		root xdr.SorobanAuthorizedInvocation
		want Confidence
	}{
		{"v1", createInvocation(deployer, nil), ConfidenceDecoded},
		{"v2_no_constructor_args", createInvocation(deployer, []xdr.ScVal{}), ConfidenceDecoded},
		{"v2_constructor_args", createInvocation(deployer, []xdr.ScVal{u32Val(1)}), ConfidencePartial},
		{"v2_opaque_constructor_arg", createInvocation(deployer, []xdr.ScVal{symVal("no good")}), ConfidenceOpaque},
		{"deploy_native_sac", deploySACInvocation(xdr.MustNewNativeAsset()), ConfidenceDecoded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exp := explainChecked(t, addressEntry(tt.root))
			if exp.Confidence != tt.want {
				t.Fatalf("confidence = %s, want %s; %+v", exp.Confidence, tt.want, exp)
			}
			if exp.Actions[0].Kind != ActionCreateContract {
				t.Fatalf("kind = %s", exp.Actions[0].Kind)
			}
			if tt.want == ConfidenceDecoded && len(exp.Unexplained) != 0 {
				t.Fatalf("decoded with notes %q", exp.Unexplained)
			}
			t.Log(exp.Actions[0].Summary)
		})
	}
}

// TestExplainFloorAcrossTree puts a decoded root over an opaque sub-invocation
// three levels down. The opaque node must pull every level above it down.
func TestExplainFloorAcrossTree(t *testing.T) {
	deep := callInvocation(contractAddress(testContractKey), "hidden", nil)
	mid := createInvocation(accountAddress(testAccountKey), nil)
	mid.SubInvocations = []xdr.SorobanAuthorizedInvocation{deep}
	root := createInvocation(accountAddress(testAccountKey), nil)
	root.SubInvocations = []xdr.SorobanAuthorizedInvocation{mid}

	exp := explainChecked(t, addressEntry(root))
	if exp.Confidence != ConfidenceOpaque || exp.Actions[0].Confidence != ConfidenceOpaque || exp.Actions[0].Sub[0].Confidence != ConfidenceOpaque {
		t.Fatalf("an opaque leaf did not floor its ancestors: %+v", exp)
	}
	if len(exp.Unexplained) != 1 || !strings.Contains(exp.Unexplained[0], "hidden") {
		t.Fatalf("unexplained = %q", exp.Unexplained)
	}
}

func TestExplainDelegates(t *testing.T) {
	for _, tt := range []struct {
		name string
		root xdr.SorobanAuthorizedInvocation
		want Confidence
	}{
		{"decoded_payload", createInvocation(accountAddress(testAccountKey), nil), ConfidenceDecoded},
		{"opaque_payload", callInvocation(contractAddress(testContractKey), "do_thing", nil), ConfidenceOpaque},
	} {
		t.Run(tt.name, func(t *testing.T) {
			exp := explainChecked(t, delegatesEntry(tt.root))
			if len(exp.Delegates) != 2 || len(exp.Delegates[0].Delegates) != 1 {
				t.Fatalf("delegate tree = %+v", exp.Delegates)
			}
			// Every delegate at every depth carries the payload's confidence:
			// shown alone, a delegate must not look more certain than what it
			// signs.
			var walk func([]Explanation)
			walk = func(ds []Explanation) {
				for _, d := range ds {
					if d.Confidence != tt.want || d.CredentialType != CredentialTypeDelegate || len(d.Actions) != 1 {
						t.Errorf("delegate %s: %+v", d.Subject, d)
					}
					walk(d.Delegates)
				}
			}
			walk(exp.Delegates)
			if !exp.Delegates[0].Signed || exp.Delegates[0].Delegates[0].Signed || !exp.Delegates[1].Signed {
				t.Fatalf("signed flags wrong: %+v", exp.Delegates)
			}
			if exp.Confidence != tt.want {
				t.Fatalf("confidence = %s, want %s", exp.Confidence, tt.want)
			}
		})
	}
}

// TestExplainFloorsAcrossDelegates checks the aggregation itself: a delegate
// less certain than the entry's own actions pulls the top level down.
func TestExplainFloorsAcrossDelegates(t *testing.T) {
	w := newWalker(DefaultMaxDepth, DefaultMaxNodes)
	nodes := []soroauth.NodeInfo{{Address: "GA", Nested: []soroauth.NodeInfo{{Address: "GB"}}}}
	ds, err := w.explainDelegates(nodes, []Action{}, ConfidencePartial, []string{"why"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if ds[0].Confidence != ConfidencePartial || ds[0].Delegates[0].Confidence != ConfidencePartial {
		t.Fatalf("delegates = %+v", ds)
	}
}

func fieldByName(a Action, name string) (Field, bool) {
	for _, f := range a.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

func sacAddress(t testing.TB, a xdr.Asset, passphrase string) xdr.ScAddress {
	t.Helper()
	addr, err := soroauth.ParseAddress(mustSAC(t, a, passphrase))
	if err != nil {
		t.Fatal(err)
	}
	return addr
}

func transferArgs() []xdr.ScVal {
	return []xdr.ScVal{addrVal(accountAddress(testAccountKey)), addrVal(accountAddress(otherKey)), i128Val(1000000000)}
}

// TestExplainTokenTransfer: a SEP-41 transfer is decoded only on the native
// SAC derived on the caller's network. Everywhere else the call shape is
// known but the asset and decimals are not: partial, with the raw amount.
func TestExplainTokenTransfer(t *testing.T) {
	nativeSAC := sacAddress(t, xdr.MustNewNativeAsset(), testnetPassphrase)
	entry := addressEntry(callInvocation(nativeSAC, "transfer", transferArgs()))

	exp := explainChecked(t, entry, WithNetwork(testnetPassphrase))
	a := exp.Actions[0]
	if exp.Confidence != ConfidenceDecoded || a.Kind != "token_transfer" || len(exp.Unexplained) != 0 {
		t.Fatalf("got %+v", exp)
	}
	if !strings.HasPrefix(a.Summary, "Transfer 100.0000000 native from G") {
		t.Fatalf("summary = %q", a.Summary)
	}
	amount, _ := fieldByName(a, "amount")
	if amount.Value != "100.0000000" || amount.Raw != "1000000000" || amount.Confidence != ConfidenceDecoded {
		t.Fatalf("amount = %+v; Raw must carry the integer from the bytes", amount)
	}

	fakeXLM := xdr.MustNewCreditAsset("XLM", usdcIssuerGAHK)
	for name, tc := range map[string]struct {
		entry xdr.SorobanAuthorizationEntry
		opts  []Option
		note  string
	}{
		"no_network":    {entry, nil, "No network passphrase was given"},
		"other_network": {entry, []Option{WithNetwork(publicPassphrase)}, "is not identified on this network"},
		"impostor":      {addressEntry(callInvocation(sacAddress(t, fakeXLM, testnetPassphrase), "transfer", transferArgs())), []Option{WithNetwork(testnetPassphrase)}, "is not identified on this network"},
		"arbitrary":     {addressEntry(callInvocation(contractAddress(testContractKey), "transfer", transferArgs())), []Option{WithNetwork(testnetPassphrase)}, "is not identified on this network"},
	} {
		t.Run(name, func(t *testing.T) {
			exp := explainChecked(t, tc.entry, tc.opts...)
			a := exp.Actions[0]
			if exp.Confidence != ConfidencePartial || a.Confidence != ConfidencePartial {
				t.Fatalf("confidence = %s, want partial", exp.Confidence)
			}
			if _, ok := fieldByName(a, "asset"); ok {
				t.Fatal("labelled without a derivation on this network")
			}
			amount, _ := fieldByName(a, "amount")
			if amount.Value != "1000000000" || amount.Raw != "" || amount.Confidence != ConfidencePartial {
				t.Fatalf("amount = %+v; want the raw integer, unscaled", amount)
			}
			if !strings.Contains(a.Summary, "units of the token at C") {
				t.Fatalf("summary = %q", a.Summary)
			}
			joined := strings.Join(exp.Unexplained, "\n")
			if !strings.Contains(joined, tc.note) || !strings.Contains(joined, "a matching signature does not show") {
				t.Fatalf("unexplained = %q", exp.Unexplained)
			}
		})
	}
}

func TestExplainTokenFunctions(t *testing.T) {
	sac := sacAddress(t, xdr.MustNewNativeAsset(), testnetPassphrase)
	a, b, c := addrVal(accountAddress(testAccountKey)), addrVal(accountAddress(otherKey)), addrVal(contractAddress(testContractKey))
	tests := []struct {
		fn   string
		args []xdr.ScVal
		want string
	}{
		{"approve", []xdr.ScVal{a, b, i128Val(5), u32Val(900)}, "Allow " + b.Address.AccountId.Address() + " to spend up to 0.0000005 native from " + a.Address.AccountId.Address() + " until ledger 900, replacing any current allowance"},
		{"transfer_from", []xdr.ScVal{c, a, b, i128Val(10000000)}, "Transfer 1.0000000 native from " + a.Address.AccountId.Address() + " to " + b.Address.AccountId.Address() + ", spending the allowance of CABAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAFNSZ"},
		{"burn", []xdr.ScVal{a, i128Val(0)}, "Burn 0.0000000 native from " + a.Address.AccountId.Address()},
		{"burn_from", []xdr.ScVal{c, a, i128Val(1)}, "Burn 0.0000001 native from " + a.Address.AccountId.Address() + ", spending the allowance of CABAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAFNSZ"},
	}
	for _, tt := range tests {
		t.Run(tt.fn, func(t *testing.T) {
			exp := explainChecked(t, addressEntry(callInvocation(sac, tt.fn, tt.args)), WithNetwork(testnetPassphrase))
			if exp.Confidence != ConfidenceDecoded || exp.Actions[0].Summary != tt.want {
				t.Fatalf("got %s %q\nwant %q", exp.Confidence, exp.Actions[0].Summary, tt.want)
			}
		})
	}
}

// TestExplainTokenNotMatched: a name alone never earns an interpretation,
// even on the real native SAC.
func TestExplainTokenNotMatched(t *testing.T) {
	sac := sacAddress(t, xdr.MustNewNativeAsset(), testnetPassphrase)
	for name, args := range map[string][]xdr.ScVal{
		"wrong_arity":       append(transferArgs(), u32Val(1)),
		"amount_not_i128":   {transferArgs()[0], transferArgs()[1], u32Val(1)},
		"address_not_value": {symVal("x"), transferArgs()[1], i128Val(1)},
	} {
		t.Run(name, func(t *testing.T) {
			exp := explainChecked(t, addressEntry(callInvocation(sac, "transfer", args)), WithNetwork(testnetPassphrase))
			a := exp.Actions[0]
			if a.Kind != ActionInvokeContract || exp.Confidence != ConfidenceOpaque {
				t.Fatalf("got %s %s", a.Kind, exp.Confidence)
			}
			if !strings.Contains(a.Summary, "the Stellar Asset Contract for native") {
				t.Fatalf("the contract's derived label should still show: %q", a.Summary)
			}
		})
	}
}

// TestExplainTokenMuxedRecipient: SEP-41's transfer accepts a MuxedAddress
// recipient, which this library does not render. The call matches, but the
// recipient is opaque, so the action is too.
func TestExplainTokenMuxedRecipient(t *testing.T) {
	sac := sacAddress(t, xdr.MustNewNativeAsset(), testnetPassphrase)
	muxed := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeMuxedAccount, MuxedAccount: &xdr.MuxedEd25519Account{Id: 5}}
	args := []xdr.ScVal{transferArgs()[0], addrVal(muxed), i128Val(1)}
	exp := explainChecked(t, addressEntry(callInvocation(sac, "transfer", args)), WithNetwork(testnetPassphrase))
	to, _ := fieldByName(exp.Actions[0], "to")
	if exp.Confidence != ConfidenceOpaque || to.Confidence != ConfidenceOpaque || to.Value != "address(muxed_account)" {
		t.Fatalf("got %s, to = %+v", exp.Confidence, to)
	}
}

func TestExplainTokenUnderOpaqueRoot(t *testing.T) {
	sac := sacAddress(t, xdr.MustNewNativeAsset(), testnetPassphrase)
	root := callInvocation(contractAddress(testContractKey), "swap", nil, callInvocation(sac, "transfer", transferArgs()))
	exp := explainChecked(t, addressEntry(root), WithNetwork(testnetPassphrase))
	if exp.Confidence != ConfidenceOpaque || exp.Actions[0].Sub[0].Confidence != ConfidenceDecoded {
		t.Fatalf("root %s, sub %s", exp.Confidence, exp.Actions[0].Sub[0].Confidence)
	}
}

// TestExplainWithAssets: WithAssets supplies candidates; the guard still
// derives and compares each one.
func TestExplainWithAssets(t *testing.T) {
	usdcA := xdr.MustNewCreditAsset("USDC", usdcIssuerGA5Z)
	usdcB := xdr.MustNewCreditAsset("USDC", usdcIssuerGAHK)
	realUSDC := addressEntry(callInvocation(sacAddress(t, usdcA, publicPassphrase), "transfer", transferArgs()))
	lookalike := addressEntry(callInvocation(sacAddress(t, usdcB, publicPassphrase), "transfer", transferArgs()))
	malformed := xdr.Asset{Type: xdr.AssetTypeAssetTypeCreditAlphanum4}

	tests := []struct {
		name      string
		entry     xdr.SorobanAuthorizationEntry
		opts      []Option
		want      Confidence
		wantAsset string
	}{
		{"candidate_derives", realUSDC, []Option{WithNetwork(publicPassphrase), WithAssets(usdcA)}, ConfidenceDecoded, "USDC:" + usdcIssuerGA5Z},
		{"no_candidates", realUSDC, []Option{WithNetwork(publicPassphrase)}, ConfidencePartial, ""},
		{"candidate_without_network", realUSDC, []Option{WithAssets(usdcA)}, ConfidencePartial, ""},
		{"candidate_on_other_network", realUSDC, []Option{WithNetwork(testnetPassphrase), WithAssets(usdcA)}, ConfidencePartial, ""},
		{"lookalike_issuer_not_labelled", lookalike, []Option{WithNetwork(publicPassphrase), WithAssets(usdcA)}, ConfidencePartial, ""},
		// The look-alike is a real SAC of its own asset; named, it is labelled
		// with its own issuer, which keeps it distinct from USDC:GA5Z.
		{"lookalike_own_asset", lookalike, []Option{WithNetwork(publicPassphrase), WithAssets(usdcB)}, ConfidenceDecoded, "USDC:" + usdcIssuerGAHK},
		{"malformed_candidate_ignored", realUSDC, []Option{WithNetwork(publicPassphrase), WithAssets(malformed, usdcA)}, ConfidenceDecoded, "USDC:" + usdcIssuerGA5Z},
		{"accumulates", realUSDC, []Option{WithNetwork(publicPassphrase), WithAssets(usdcB), WithAssets(usdcA)}, ConfidenceDecoded, "USDC:" + usdcIssuerGA5Z},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exp := explainChecked(t, tt.entry, tt.opts...)
			a := exp.Actions[0]
			asset, hasAsset := fieldByName(a, "asset")
			if exp.Confidence != tt.want || asset.Value != tt.wantAsset || hasAsset != (tt.wantAsset != "") {
				t.Fatalf("got %s, asset %q; want %s, %q", exp.Confidence, asset.Value, tt.want, tt.wantAsset)
			}
			if tt.want == ConfidenceDecoded && !strings.HasPrefix(a.Summary, "Transfer 100.0000000 "+tt.wantAsset+" from ") {
				t.Fatalf("summary = %q", a.Summary)
			}
		})
	}
}

// TestSACDecimalsOnlyOnSACs: the host's 7 decimal places are a fact about
// Stellar Asset Contracts only. No matching call on any other contract is
// scaled, under any combination of options.
func TestSACDecimalsOnlyOnSACs(t *testing.T) {
	usdcA := xdr.MustNewCreditAsset("USDC", usdcIssuerGA5Z)
	contracts := map[string]xdr.ScAddress{
		"arbitrary":           contractAddress(testContractKey),
		"issued_XLM_impostor": sacAddress(t, xdr.MustNewCreditAsset("XLM", usdcIssuerGAHK), testnetPassphrase),
		"public_native_sac":   sacAddress(t, xdr.MustNewNativeAsset(), publicPassphrase),
		"usdc_lookalike":      sacAddress(t, xdr.MustNewCreditAsset("USDC", usdcIssuerGAHK), publicPassphrase),
	}
	optionSets := map[string][]Option{
		"none":           nil,
		"testnet":        {WithNetwork(testnetPassphrase)},
		"testnet_assets": {WithNetwork(testnetPassphrase), WithAssets(usdcA)},
		"public_assets":  {WithNetwork(publicPassphrase), WithAssets(usdcA)},
	}
	for cname, c := range contracts {
		for oname, opts := range optionSets {
			if cname == "public_native_sac" && strings.HasPrefix(oname, "public") {
				continue // it is the real native SAC on public; scaling is right there
			}
			t.Run(cname+"/"+oname, func(t *testing.T) {
				exp := explainChecked(t, addressEntry(callInvocation(c, "transfer", transferArgs())), opts...)
				amount, _ := fieldByName(exp.Actions[0], "amount")
				if amount.Value != "1000000000" || amount.Raw != "" || strings.Contains(exp.Actions[0].Summary, ".") {
					t.Fatalf("scaled on a contract that is not a derived SAC: amount %+v, summary %q", amount, exp.Actions[0].Summary)
				}
			})
		}
	}
}

func TestScaleDecimal(t *testing.T) {
	for in, want := range map[string]string{
		"0":          "0.0000000",
		"1":          "0.0000001",
		"-1":         "-0.0000001",
		"10000000":   "1.0000000",
		"1000000000": "100.0000000",
		"-123456789": "-12.3456789",
		"170141183460469231731687303715884105727":  "17014118346046923173168730371588.4105727",
		"-170141183460469231731687303715884105728": "-17014118346046923173168730371588.4105728",
	} {
		if got := scaleDecimal(in, 7); got != want {
			t.Errorf("scaleDecimal(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestExplainErrors(t *testing.T) {
	contract := contractAddress(testContractKey)
	nilArmSub := xdr.SorobanAuthorizedInvocation{Function: xdr.SorobanAuthorizedFunction{
		Type: xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn}}

	tests := []struct {
		name  string
		entry xdr.SorobanAuthorizationEntry
		opts  []Option
		want  error
	}{
		{"unsupported_credentials", xdr.SorobanAuthorizationEntry{
			Credentials:    xdr.SorobanCredentials{Type: xdr.SorobanCredentialsType(99)},
			RootInvocation: callInvocation(contract, "f", nil)}, nil, ErrUnsupportedCredentials},
		{"unsupported_credentials_matches_soroauth", xdr.SorobanAuthorizationEntry{
			Credentials:    xdr.SorobanCredentials{Type: xdr.SorobanCredentialsType(99)},
			RootInvocation: callInvocation(contract, "f", nil)}, nil, soroauth.ErrUnsupportedCredentials},
		{"empty_sub_invocation_arm", addressEntry(callInvocation(contract, "f", nil, nilArmSub)), nil, ErrEmptyArm},
		{"empty_arg_arm", addressEntry(callInvocation(contract, "f", []xdr.ScVal{{Type: xdr.ScValTypeScvI128}})), nil, ErrEmptyArm},
		{"depth_limit_in_tree", addressEntry(callInvocation(contract, "f", nil,
			callInvocation(contract, "g", nil, callInvocation(contract, "h", nil)))), []Option{WithMaxDepth(2)}, ErrDepthLimit},
		{"depth_limit_in_args", addressEntry(callInvocation(contract, "f", []xdr.ScVal{nested(3)})), []Option{WithMaxDepth(3)}, ErrDepthLimit},
		{"node_limit", addressEntry(callInvocation(contract, "f", []xdr.ScVal{u32Val(1), u32Val(2)})), []Option{WithMaxNodes(2)}, ErrNodeLimit},
		{"node_limit_in_delegates", delegatesEntry(callInvocation(contract, "f", nil)), []Option{WithMaxNodes(3)}, ErrNodeLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Explain(tt.entry, tt.opts...)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if !strings.HasPrefix(err.Error(), "explain: ") {
				t.Fatalf("error %q is not wrapped with the package prefix", err)
			}
		})
	}
}

func TestExplainLimitsFitExactly(t *testing.T) {
	contract := contractAddress(testContractKey)
	// Root at depth 1, its argument at depth 2, nested(3) reaching depth 4.
	if _, err := Explain(addressEntry(callInvocation(contract, "f", []xdr.ScVal{nested(3)})), WithMaxDepth(4)); err != nil {
		t.Fatalf("depth 4 under limit 4: %v", err)
	}
	// One invocation plus two arguments is three nodes.
	if _, err := Explain(addressEntry(callInvocation(contract, "f", []xdr.ScVal{u32Val(1), u32Val(2)})), WithMaxNodes(3)); err != nil {
		t.Fatalf("3 nodes under limit 3: %v", err)
	}
}

func TestExplainInvalidOptions(t *testing.T) {
	entry := addressEntry(callInvocation(contractAddress(testContractKey), "f", nil))
	for name, opt := range map[string]Option{"depth": WithMaxDepth(0), "nodes": WithMaxNodes(-1)} {
		t.Run(name, func(t *testing.T) {
			if _, err := Explain(entry, opt); err == nil {
				t.Fatal("accepted a non-positive limit")
			}
		})
	}
}

func TestArgumentsShownRaw(t *testing.T) {
	for n, want := range map[int]string{
		0: "the call passes no arguments",
		1: "its 1 argument is shown as a raw value",
		2: "its 2 arguments are shown as raw values",
	} {
		if got := argumentsShownRaw(n); got != want {
			t.Errorf("argumentsShownRaw(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestExplainHundredArguments(t *testing.T) {
	args := make([]xdr.ScVal, 100)
	for i := range args {
		args[i] = u32Val(uint32(i))
	}
	exp := explainChecked(t, addressEntry(callInvocation(contractAddress(testContractKey), "wide", args)))
	if len(exp.Actions[0].Fields) != 103 || !strings.HasSuffix(exp.Actions[0].Summary, "with 100 arguments") {
		t.Fatalf("fields = %d, summary = %q", len(exp.Actions[0].Fields), exp.Actions[0].Summary)
	}
}

func TestExplainDeterministic(t *testing.T) {
	entry := delegatesEntry(callInvocation(contractAddress(testContractKey), "do_thing",
		[]xdr.ScVal{mapVal(xdr.ScMapEntry{Key: symVal("b"), Val: symVal("bad sym")}, xdr.ScMapEntry{Key: symVal("a"), Val: u32Val(1)})}))
	first := explainChecked(t, entry)
	for i := 0; i < 50; i++ {
		again := explainChecked(t, entry)
		if strings.Join(again.Unexplained, "|") != strings.Join(first.Unexplained, "|") || again.Actions[0].Summary != first.Actions[0].Summary {
			t.Fatal("two runs over the same entry differ")
		}
	}
}
