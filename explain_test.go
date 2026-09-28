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

func TestExplainSourceAccount(t *testing.T) {
	exp := explainChecked(t, sourceAccountEntry(callInvocation(contractAddress(testContractKey), "do_thing", nil)))
	if exp.Confidence != ConfidenceDecoded || exp.CredentialType != soroauth.CredentialTypeSourceAccount {
		t.Fatalf("got %+v", exp)
	}
	if exp.Subject != "" || exp.Signed || exp.Nonce != 0 || len(exp.Actions) != 0 || exp.Actions == nil {
		t.Fatalf("source-account explanation carries fields it has no basis for: %+v", exp)
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

func TestExplainAssetLabelNeedsNetwork(t *testing.T) {
	nativeTestnet := mustSAC(t, xdr.MustNewNativeAsset(), testnetPassphrase)
	sacAddr, err := soroauth.ParseAddress(nativeTestnet)
	if err != nil {
		t.Fatal(err)
	}
	entry := addressEntry(callInvocation(sacAddr, "transfer",
		[]xdr.ScVal{addrVal(accountAddress(testAccountKey)), addrVal(accountAddress(otherKey)), i128Val(1000000000)}))

	labelled := explainChecked(t, entry, WithNetwork(testnetPassphrase))
	if !strings.Contains(labelled.Actions[0].Summary, "the Stellar Asset Contract for native") {
		t.Fatalf("real native SAC not labelled on its own network: %q", labelled.Actions[0].Summary)
	}
	// Labelling the contract does not interpret the call.
	if labelled.Confidence != ConfidenceOpaque {
		t.Fatalf("confidence = %s, want opaque until the token interface is registered", labelled.Confidence)
	}

	for name, opts := range map[string][]Option{
		"no_network":    nil,
		"other_network": {WithNetwork(publicPassphrase)},
	} {
		t.Run(name, func(t *testing.T) {
			exp := explainChecked(t, entry, opts...)
			for _, f := range exp.Actions[0].Fields {
				if f.Name == "asset" {
					t.Fatalf("labelled %q without a derivation on this network", f.Value)
				}
			}
			if strings.Contains(exp.Actions[0].Summary, "native") {
				t.Fatalf("summary = %q", exp.Actions[0].Summary)
			}
		})
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
