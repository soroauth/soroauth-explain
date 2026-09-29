package explain

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/soroauth/soroauth-explain/spec"
	soroauth "github.com/soroauth/soroauth-go"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// oracleContract runs the code recorded in spec/testdata/oracle_testnet.wasm.
const oracleContract = "CAAL5BZAQ3W2G5CZX4HVLWNQIJISQ62ZNXXYCX2LONG4FFLRRYEYONZC"

func oracleSpec(t testing.TB) spec.Spec {
	t.Helper()
	wasm, err := os.ReadFile("spec/testdata/oracle_testnet.wasm")
	if err != nil {
		t.Fatal(err)
	}
	s, err := spec.FromWasm(wasm)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func builtSpec(t testing.TB, entries ...xdr.ScSpecEntry) spec.Spec {
	t.Helper()
	var buf bytes.Buffer
	for _, e := range entries {
		b, err := e.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(b)
	}
	s, err := spec.Parse(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func specFn(name string, inputs ...xdr.ScSpecFunctionInputV0) xdr.ScSpecEntry {
	return xdr.ScSpecEntry{Kind: xdr.ScSpecEntryKindScSpecEntryFunctionV0, FunctionV0: &xdr.ScSpecFunctionV0{Name: xdr.ScSymbol(name), Inputs: inputs}}
}

func specIn(name string, typ xdr.ScSpecType) xdr.ScSpecFunctionInputV0 {
	return xdr.ScSpecFunctionInputV0{Name: name, Type: xdr.ScSpecTypeDef{Type: typ}}
}

func mustAddr(t testing.TB, s string) xdr.ScAddress {
	t.Helper()
	a, err := soroauth.ParseAddress(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestExplainWithSpecNamesArguments(t *testing.T) {
	oracle := mustAddr(t, oracleContract)
	specs := map[string]spec.Spec{oracleContract: oracleSpec(t)}
	setPrice := []xdr.ScVal{addrVal(accountAddress(testAccountKey)), symVal("BTC"), i128Val(6512345678901)}

	exp := explainChecked(t, addressEntry(callInvocation(oracle, "set_price", setPrice)), WithNetwork(testnetPassphrase), WithSpecs(specs))
	a := exp.Actions[0]
	if exp.Confidence != ConfidencePartial || a.Kind != ActionInvokeContract {
		t.Fatalf("got %s %s", exp.Confidence, a.Kind)
	}
	for _, want := range []string{"arg[0]:caller", "arg[1]:symbol", "arg[2]:price"} {
		if _, ok := fieldByName(a, want); !ok {
			t.Errorf("no field %s in %+v", want, a.Fields)
		}
	}
	if price, _ := fieldByName(a, "arg[2]:price"); price.Value != "i128(6512345678901)" || price.Raw != "" {
		t.Fatalf("price = %+v; the value must stay raw", price)
	}
	if !strings.HasSuffix(a.Summary, "with 3 arguments, named by the spec the contract publishes") {
		t.Fatalf("summary = %q", a.Summary)
	}
	if n := strings.Join(exp.Unexplained, "\n"); !strings.Contains(n, "come from the spec the contract publishes") || strings.Contains(n, "is not interpreted by this library") {
		t.Fatalf("unexplained = %q", exp.Unexplained)
	}

	// Without the spec, the same call is opaque, as before.
	if plain := explainChecked(t, addressEntry(callInvocation(oracle, "set_price", setPrice)), WithNetwork(testnetPassphrase)); plain.Confidence != ConfidenceOpaque {
		t.Fatalf("without a spec: %s", plain.Confidence)
	}
}

func TestExplainWithSpecNotApplied(t *testing.T) {
	oracle := mustAddr(t, oracleContract)
	specs := map[string]spec.Spec{oracleContract: oracleSpec(t)}
	acct := addrVal(accountAddress(testAccountKey))
	tests := []struct {
		name     string
		contract xdr.ScAddress
		fn       string
		args     []xdr.ScVal
		note     string
	}{
		{"wrong_types", oracle, "set_price", []xdr.ScVal{acct, symVal("BTC"), symVal("high")}, "declares set_price with different arguments"},
		{"wrong_arity", oracle, "set_price", []xdr.ScVal{acct, symVal("BTC")}, "declares set_price with different arguments"},
		{"not_declared", oracle, "not_in_spec", []xdr.ScVal{acct}, ""},
		{"other_contract", contractAddress(testContractKey), "set_price", []xdr.ScVal{acct, symVal("BTC"), i128Val(1)}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exp := explainChecked(t, addressEntry(callInvocation(tt.contract, tt.fn, tt.args)), WithSpecs(specs))
			if exp.Confidence != ConfidenceOpaque {
				t.Fatalf("confidence %s, want opaque", exp.Confidence)
			}
			for _, f := range exp.Actions[0].Fields {
				if strings.Contains(f.Name, ":") {
					t.Fatalf("argument named %s from a spec that does not describe this call", f.Name)
				}
			}
			n := strings.Join(exp.Unexplained, "\n")
			if (tt.note != "") != strings.Contains(n, "different arguments") || (tt.note != "" && !strings.Contains(n, tt.note)) {
				t.Fatalf("unexplained = %q", exp.Unexplained)
			}
		})
	}
}

// TestExplainWithSpecNeverDecodes: a spec's names are the contract's own
// claims. Misleading names earn no scaling and no label, a known interface
// keeps precedence, and a spec on a real SAC still leaves an unknown
// function partial.
func TestExplainWithSpecNeverDecodes(t *testing.T) {
	arbitrary := contractAddress(testContractKey)
	misleading := builtSpec(t,
		specFn("do_thing", specIn("xlm_amount", xdr.ScSpecTypeScSpecTypeI128), specIn("recipient", xdr.ScSpecTypeScSpecTypeAddress)),
		specFn("transfer", specIn("x", xdr.ScSpecTypeScSpecTypeAddress), specIn("y", xdr.ScSpecTypeScSpecTypeAddress), specIn("z", xdr.ScSpecTypeScSpecTypeI128)),
		specFn("weird", specIn("a\x1b[31mb", xdr.ScSpecTypeScSpecTypeU32)),
	)
	nativeSAC := sacAddress(t, xdr.MustNewNativeAsset(), testnetPassphrase)
	sacSpec := builtSpec(t, specFn("mint", specIn("to", xdr.ScSpecTypeScSpecTypeAddress), specIn("amount", xdr.ScSpecTypeScSpecTypeI128)))
	nativeSACStr, _ := soroauth.FormatAddress(nativeSAC)
	arbitraryStr, _ := soroauth.FormatAddress(arbitrary)
	specs := map[string]spec.Spec{arbitraryStr: misleading, nativeSACStr: sacSpec}
	opts := []Option{WithNetwork(testnetPassphrase), WithSpecs(specs)}

	t.Run("misleading_names", func(t *testing.T) {
		exp := explainChecked(t, addressEntry(callInvocation(arbitrary, "do_thing", []xdr.ScVal{i128Val(1000000000), addrVal(accountAddress(otherKey))})), opts...)
		a := exp.Actions[0]
		amt, _ := fieldByName(a, "arg[0]:xlm_amount")
		if exp.Confidence != ConfidencePartial || amt.Value != "i128(1000000000)" || strings.Contains(a.Summary, ".") {
			t.Fatalf("got %s, %+v, %q", exp.Confidence, amt, a.Summary)
		}
		if _, ok := fieldByName(a, "asset"); ok {
			t.Fatal("a spec name earned an asset label")
		}
	})
	t.Run("registry_first", func(t *testing.T) {
		exp := explainChecked(t, addressEntry(callInvocation(arbitrary, "transfer", transferArgs())), opts...)
		if exp.Actions[0].Kind != "token_transfer" || exp.Confidence != ConfidencePartial {
			t.Fatalf("got %s %s", exp.Actions[0].Kind, exp.Confidence)
		}
		if _, ok := fieldByName(exp.Actions[0], "arg[0]:x"); ok {
			t.Fatal("a spec renamed a registered interface's arguments")
		}
	})
	t.Run("unplain_name", func(t *testing.T) {
		exp := explainChecked(t, addressEntry(callInvocation(arbitrary, "weird", []xdr.ScVal{u32Val(1)})), opts...)
		if _, ok := fieldByName(exp.Actions[0], "arg[0]"); !ok || !strings.Contains(strings.Join(exp.Unexplained, "\n"), "which is not shown") {
			t.Fatalf("got %+v %q", exp.Actions[0].Fields, exp.Unexplained)
		}
		for _, f := range exp.Actions[0].Fields {
			if strings.Contains(f.Name, "\x1b") {
				t.Fatal("contract-supplied control characters reached a field name")
			}
		}
	})
	t.Run("sac_unknown_function", func(t *testing.T) {
		exp := explainChecked(t, addressEntry(callInvocation(nativeSAC, "mint", []xdr.ScVal{addrVal(accountAddress(otherKey)), i128Val(10000000)})), opts...)
		a := exp.Actions[0]
		if exp.Confidence != ConfidencePartial || !strings.Contains(a.Summary, "the Stellar Asset Contract for native") {
			t.Fatalf("got %s %q", exp.Confidence, a.Summary)
		}
		if amt, _ := fieldByName(a, "arg[1]:amount"); amt.Value != "i128(10000000)" {
			t.Fatalf("amount = %+v; a spec-named argument is never scaled", amt)
		}
	})
}

func TestWithSpecsAccumulates(t *testing.T) {
	a := builtSpec(t, specFn("f", specIn("x", xdr.ScSpecTypeScSpecTypeU32)))
	b := builtSpec(t, specFn("f", specIn("y", xdr.ScSpecTypeScSpecTypeU32)))
	c, _ := soroauth.FormatAddress(contractAddress(testContractKey))
	exp := explainChecked(t, addressEntry(callInvocation(contractAddress(testContractKey), "f", []xdr.ScVal{u32Val(1)})),
		WithSpecs(map[string]spec.Spec{c: a}), WithSpecs(map[string]spec.Spec{c: b}))
	if _, ok := fieldByName(exp.Actions[0], "arg[0]:y"); !ok {
		t.Fatalf("later spec did not replace the earlier: %+v", exp.Actions[0].Fields)
	}
	// The caller's map is copied, not kept.
	m := map[string]spec.Spec{c: a}
	opt := WithSpecs(m)
	delete(m, c)
	exp = explainChecked(t, addressEntry(callInvocation(contractAddress(testContractKey), "f", []xdr.ScVal{u32Val(1)})), opt)
	if _, ok := fieldByName(exp.Actions[0], "arg[0]:x"); !ok {
		t.Fatal("WithSpecs aliased the caller's map")
	}
}
