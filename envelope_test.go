package explain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

func muxed(key [32]byte) xdr.MuxedAccount {
	k := xdr.Uint256(key)
	return xdr.MuxedAccount{Type: xdr.CryptoKeyTypeKeyTypeEd25519, Ed25519: &k}
}

func invokeOp(hf xdr.HostFunction, auth ...xdr.SorobanAuthorizationEntry) xdr.Operation {
	return xdr.Operation{Body: xdr.OperationBody{
		Type:                 xdr.OperationTypeInvokeHostFunction,
		InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{HostFunction: hf, Auth: auth},
	}}
}

func invokeContract(contract xdr.ScAddress, fn string, args []xdr.ScVal) xdr.HostFunction {
	return xdr.HostFunction{
		Type:           xdr.HostFunctionTypeHostFunctionTypeInvokeContract,
		InvokeContract: &xdr.InvokeContractArgs{ContractAddress: contract, FunctionName: xdr.ScSymbol(fn), Args: args},
	}
}

func txEnvelope(ops ...xdr.Operation) xdr.TransactionEnvelope {
	return xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{Tx: xdr.Transaction{
			SourceAccount: muxed(testAccountKey),
			Fee:           100,
			SeqNum:        7,
			Memo:          xdr.Memo{Type: xdr.MemoTypeMemoNone},
			Cond:          xdr.Preconditions{Type: xdr.PreconditionTypePrecondNone},
			Operations:    ops,
		}},
	}
}

// checkEnvelopeInvariants asserts what every EnvelopeExplanation obeys: its
// confidence is the floor of its operations, each operation's is the floor
// of its invocation and entries, and anything not decoded says why in the
// envelope's own Unexplained.
func checkEnvelopeInvariants(t testing.TB, env EnvelopeExplanation) {
	t.Helper()
	if env.Confidence != ConfidenceDecoded && len(env.Unexplained) == 0 {
		t.Errorf("%s envelope has an empty Unexplained", env.Confidence)
	}
	if env.Confidence == ConfidenceDecoded && len(env.Unexplained) != 0 {
		t.Errorf("decoded envelope has Unexplained %q", env.Unexplained)
	}
	want := ConfidenceDecoded
	if len(env.Operations) == 0 {
		want = ConfidenceOpaque
	}
	for _, op := range env.Operations {
		opWant := ConfidenceDecoded
		if op.Type != "InvokeHostFunction" {
			opWant = ConfidenceOpaque
		}
		if op.Invocation != nil {
			checkAction(t, *op.Invocation)
			opWant = Floor(opWant, op.Invocation.Confidence)
		}
		for _, e := range op.Entries {
			checkInvariants(t, e)
			opWant = Floor(opWant, e.Confidence)
		}
		if op.Confidence != opWant {
			t.Errorf("operation %d confidence = %s, floor of its parts is %s", op.Index, op.Confidence, opWant)
		}
		want = Floor(want, op.Confidence)
	}
	if env.Confidence != want {
		t.Errorf("envelope confidence = %s, floor of its operations is %s", env.Confidence, want)
	}
	for _, f := range env.Transaction {
		if f.Confidence != ConfidenceDecoded {
			t.Errorf("transaction field %s is %s", f.Name, f.Confidence)
		}
	}
}

func explainEnvelopeChecked(t testing.TB, env xdr.TransactionEnvelope, opts ...Option) EnvelopeExplanation {
	t.Helper()
	before, err := env.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	out, err := ExplainEnvelope(env, opts...)
	if err != nil {
		t.Fatalf("ExplainEnvelope: %v", err)
	}
	after, err := env.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("ExplainEnvelope modified the caller's envelope")
	}
	checkEnvelopeInvariants(t, out)
	return out
}

func transactionField(env EnvelopeExplanation, name string) (string, bool) {
	for _, f := range env.Transaction {
		if f.Name == name {
			return f.Value, true
		}
	}
	return "", false
}

// TestExplainEnvelopeSACTransfer: a native SAC transfer invoked by the
// transaction and authorized by a source-account entry is decoded end to
// end, and the transaction's own fields are read from the bytes.
func TestExplainEnvelopeSACTransfer(t *testing.T) {
	nativeSAC := sacAddress(t, xdr.MustNewNativeAsset(), testnetPassphrase)
	env := txEnvelope(invokeOp(invokeContract(nativeSAC, "transfer", transferArgs()),
		sourceAccountEntry(callInvocation(nativeSAC, "transfer", transferArgs()))))

	out := explainEnvelopeChecked(t, env, WithNetwork(testnetPassphrase))
	if out.Confidence != ConfidenceDecoded {
		t.Fatalf("confidence = %s, want decoded; unexplained %q", out.Confidence, out.Unexplained)
	}
	op := out.Operations[0]
	if op.Invocation == nil || op.Invocation.Kind != "token_transfer" || len(op.Entries) != 1 {
		t.Fatalf("operation = %+v", op)
	}
	src, _ := transactionField(out, "source")
	if want := muxed(testAccountKey); src != want.Address() {
		t.Errorf("source = %q, want %q", src, want.Address())
	}
	for name, want := range map[string]string{"envelope": "transaction", "fee": "100 stroops", "sequence": "7"} {
		if got, _ := transactionField(out, name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if _, ok := transactionField(out, "memo"); ok {
		t.Error("a transaction with no memo has a memo field")
	}
}

// TestExplainEnvelopeInvocationFloors is the envelope's form of the honesty
// boundary: decoded entries never make an envelope decoded while the call the
// transaction itself makes is not. A reader approving the envelope approves
// that call too, and must be shown it.
func TestExplainEnvelopeInvocationFloors(t *testing.T) {
	nativeSAC := sacAddress(t, xdr.MustNewNativeAsset(), testnetPassphrase)
	env := txEnvelope(invokeOp(invokeContract(contractAddress(testContractKey), "do_thing", []xdr.ScVal{u32Val(1)}),
		addressEntry(callInvocation(nativeSAC, "transfer", transferArgs()))))

	out := explainEnvelopeChecked(t, env, WithNetwork(testnetPassphrase))
	if out.Operations[0].Entries[0].Confidence != ConfidenceDecoded {
		t.Fatalf("entry = %s, want decoded", out.Operations[0].Entries[0].Confidence)
	}
	if out.Confidence != ConfidenceOpaque {
		t.Fatalf("confidence = %s, want opaque", out.Confidence)
	}
	if len(out.Unexplained) == 0 || !strings.HasPrefix(out.Unexplained[0], "Operation 0: invocation: The function do_thing") {
		t.Errorf("unexplained = %q", out.Unexplained)
	}
}

// TestExplainEnvelopeEntryNotesPrefixed: an entry's reasons reach the
// envelope's list, saying which operation and entry they belong to.
func TestExplainEnvelopeEntryNotesPrefixed(t *testing.T) {
	nativeSAC := sacAddress(t, xdr.MustNewNativeAsset(), testnetPassphrase)
	opaque := contractAddress(testContractKey)
	env := txEnvelope(invokeOp(invokeContract(nativeSAC, "transfer", transferArgs()),
		addressEntry(callInvocation(nativeSAC, "transfer", transferArgs())),
		addressEntry(callInvocation(opaque, "do_thing", nil))))

	out := explainEnvelopeChecked(t, env, WithNetwork(testnetPassphrase))
	if out.Confidence != ConfidenceOpaque {
		t.Fatalf("confidence = %s, want opaque", out.Confidence)
	}
	want := "Operation 0: entry 1: " + out.Operations[0].Entries[1].Unexplained[0]
	if len(out.Unexplained) != 1 || out.Unexplained[0] != want {
		t.Errorf("unexplained = %q, want [%q]", out.Unexplained, want)
	}
}

// TestExplainEnvelopeOtherOperations: an operation this library does not
// interpret is named and opaque, never dropped.
func TestExplainEnvelopeOtherOperations(t *testing.T) {
	dest := muxed(otherKey)
	payment := xdr.Operation{Body: xdr.OperationBody{
		Type:      xdr.OperationTypePayment,
		PaymentOp: &xdr.PaymentOp{Destination: dest, Asset: xdr.MustNewNativeAsset(), Amount: 5},
	}}
	src := muxed(delegateKeyA)
	payment.SourceAccount = &src
	restore := xdr.Operation{Body: xdr.OperationBody{
		Type:               xdr.OperationTypeRestoreFootprint,
		RestoreFootprintOp: &xdr.RestoreFootprintOp{},
	}}

	out := explainEnvelopeChecked(t, txEnvelope(payment, restore))
	if out.Confidence != ConfidenceOpaque || len(out.Operations) != 2 {
		t.Fatalf("got %s with %d operations", out.Confidence, len(out.Operations))
	}
	if out.Operations[0].Type != "Payment" || out.Operations[0].Source != src.Address() {
		t.Errorf("operation 0 = %+v", out.Operations[0])
	}
	want := []string{
		"Operation 0: a Payment operation, which this library does not interpret.",
		"Operation 1: a RestoreFootprint operation, which acts on the transaction's footprint; this library does not show the footprint.",
	}
	if strings.Join(out.Unexplained, "\n") != strings.Join(want, "\n") {
		t.Errorf("unexplained = %q, want %q", out.Unexplained, want)
	}
}

func TestExplainEnvelopeNoOperations(t *testing.T) {
	out := explainEnvelopeChecked(t, txEnvelope())
	if out.Confidence != ConfidenceOpaque || len(out.Operations) != 0 || len(out.Unexplained) != 1 {
		t.Errorf("got %+v", out)
	}
}

// TestExplainEnvelopeTransactionFields covers each envelope type, memo type
// and precondition the transaction can carry.
func TestExplainEnvelopeTransactionFields(t *testing.T) {
	op := invokeOp(invokeContract(contractAddress(testContractKey), "f", nil))
	text := "hi\x1b[31m"
	var id xdr.Uint64 = 9
	hash := xdr.Hash{0xab}
	minSeq := xdr.SequenceNumber(3)
	signer := xdr.SignerKey{Type: xdr.SignerKeyTypeSignerKeyTypeEd25519, Ed25519: (*xdr.Uint256)(&otherKey)}
	signerAddr := signer.Address()

	v0 := xdr.TransactionEnvelope{Type: xdr.EnvelopeTypeEnvelopeTypeTxV0, V0: &xdr.TransactionV0Envelope{Tx: xdr.TransactionV0{
		SourceAccountEd25519: xdr.Uint256(testAccountKey), Fee: 200, SeqNum: 11,
		TimeBounds: &xdr.TimeBounds{MinTime: 1, MaxTime: 2},
		Memo:       xdr.Memo{Type: xdr.MemoTypeMemoId, Id: &id},
		Operations: []xdr.Operation{op},
	}}}

	withText := txEnvelope(op)
	withText.V1.Tx.Memo = xdr.Memo{Type: xdr.MemoTypeMemoText, Text: &text}
	withText.V1.Tx.Cond = xdr.Preconditions{Type: xdr.PreconditionTypePrecondTime, TimeBounds: &xdr.TimeBounds{MinTime: 0, MaxTime: 99}}

	withV2 := txEnvelope(op)
	withV2.V1.Tx.Memo = xdr.Memo{Type: xdr.MemoTypeMemoHash, Hash: &hash}
	withV2.V1.Tx.Cond = xdr.Preconditions{Type: xdr.PreconditionTypePrecondV2, V2: &xdr.PreconditionsV2{
		LedgerBounds:    &xdr.LedgerBounds{MinLedger: 10, MaxLedger: 20},
		MinSeqNum:       &minSeq,
		MinSeqAge:       60,
		MinSeqLedgerGap: 4,
		ExtraSigners:    []xdr.SignerKey{signer},
	}}

	inner := txEnvelope(op)
	feeBump := xdr.TransactionEnvelope{Type: xdr.EnvelopeTypeEnvelopeTypeTxFeeBump, FeeBump: &xdr.FeeBumpTransactionEnvelope{Tx: xdr.FeeBumpTransaction{
		FeeSource: muxed(otherKey),
		Fee:       5000,
		InnerTx:   xdr.FeeBumpTransactionInnerTx{Type: xdr.EnvelopeTypeEnvelopeTypeTx, V1: inner.V1},
	}}}

	fee := muxed(otherKey)
	cases := []struct {
		name string
		env  xdr.TransactionEnvelope
		want map[string]string
	}{
		{"v0", v0, map[string]string{"envelope": "transaction (v0)", "fee": "200 stroops", "sequence": "11",
			"memo": "id 9", "time_bounds": "1..2 (unix seconds)"}},
		{"text memo quoted", withText, map[string]string{"memo": `text "hi\x1b[31m"`, "time_bounds": "0..99 (unix seconds)"}},
		{"v2 preconditions", withV2, map[string]string{"memo": "hash " + hex.EncodeToString(hash[:]),
			"ledger_bounds": "10..20", "min_sequence": "3", "min_sequence_age": "60 seconds",
			"min_sequence_ledger_gap": "4", "extra_signer[0]": signerAddr}},
		{"fee bump", feeBump, map[string]string{"envelope": "fee bump transaction", "fee_source": fee.Address(),
			"fee_bump_fee": "5000 stroops", "fee": "100 stroops", "sequence": "7"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := explainEnvelopeChecked(t, tc.env)
			for name, want := range tc.want {
				if got, ok := transactionField(out, name); got != want {
					t.Errorf("%s = %q (present %v), want %q", name, got, ok, want)
				}
			}
		})
	}
}

// TestExplainEnvelopeHostFunctions: creations read like their authorized
// counterparts, and an upload is described by a size and hash derived here.
func TestExplainEnvelopeHostFunctions(t *testing.T) {
	code := []byte("\x00asm\x01\x00\x00\x00")
	sum := sha256.Sum256(code)
	created := createInvocation(accountAddress(testAccountKey), nil).Function.CreateContractHostFn
	createdV2 := createInvocation(accountAddress(testAccountKey), []xdr.ScVal{u32Val(1)}).Function.CreateContractV2HostFn

	cases := []struct {
		name string
		hf   xdr.HostFunction
		kind ActionKind
		conf Confidence
	}{
		{"upload", xdr.HostFunction{Type: xdr.HostFunctionTypeHostFunctionTypeUploadContractWasm, Wasm: &code}, ActionUploadWasm, ConfidenceDecoded},
		{"create", xdr.HostFunction{Type: xdr.HostFunctionTypeHostFunctionTypeCreateContract, CreateContract: created}, ActionCreateContract, ConfidenceDecoded},
		{"create v2", xdr.HostFunction{Type: xdr.HostFunctionTypeHostFunctionTypeCreateContractV2, CreateContractV2: createdV2}, ActionCreateContract, ConfidencePartial},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := explainEnvelopeChecked(t, txEnvelope(invokeOp(tc.hf)))
			inv := out.Operations[0].Invocation
			if inv.Kind != tc.kind || inv.Confidence != tc.conf {
				t.Fatalf("invocation = %s %s, want %s %s", inv.Kind, inv.Confidence, tc.kind, tc.conf)
			}
		})
	}

	out := explainEnvelopeChecked(t, txEnvelope(invokeOp(cases[0].hf)))
	if got, want := out.Operations[0].Invocation.Summary, "Upload 8 bytes of contract code with hash "+hex.EncodeToString(sum[:]); got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

// TestExplainEnvelopeNodeBudgetShared: the node limit bounds the envelope as
// a whole. Two entries that each fit alone do not fit together.
func TestExplainEnvelopeNodeBudgetShared(t *testing.T) {
	c := contractAddress(testContractKey)
	entry := addressEntry(callInvocation(c, "f", []xdr.ScVal{u32Val(1), u32Val(2)}))
	if _, err := Explain(entry, WithMaxNodes(3)); err != nil {
		t.Fatalf("one entry alone: %v", err)
	}
	hf := xdr.HostFunction{Type: xdr.HostFunctionTypeHostFunctionTypeUploadContractWasm, Wasm: &[]byte{}}
	// One node for the upload, three for each entry.
	_, err := ExplainEnvelope(txEnvelope(invokeOp(hf, entry, entry)), WithMaxNodes(6))
	if !errors.Is(err, ErrNodeLimit) {
		t.Fatalf("err = %v, want ErrNodeLimit", err)
	}
	if _, err := ExplainEnvelope(txEnvelope(invokeOp(hf, entry, entry)), WithMaxNodes(7)); err != nil {
		t.Fatalf("exactly at the limit: %v", err)
	}
}

func TestExplainEnvelopeErrors(t *testing.T) {
	unsupported := addressEntry(callInvocation(contractAddress(testContractKey), "f", nil))
	unsupported.Credentials.Type = 99

	cases := []struct {
		name string
		env  xdr.TransactionEnvelope
		opts []Option
		want error
	}{
		{"empty v1 arm", xdr.TransactionEnvelope{Type: xdr.EnvelopeTypeEnvelopeTypeTx}, nil, ErrEmptyArm},
		{"empty invoke op", txEnvelope(xdr.Operation{Body: xdr.OperationBody{Type: xdr.OperationTypeInvokeHostFunction}}), nil, ErrEmptyArm},
		{"empty host function", txEnvelope(invokeOp(xdr.HostFunction{Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract})), nil, ErrEmptyArm},
		{"unsupported entry", txEnvelope(invokeOp(invokeContract(contractAddress(testContractKey), "f", nil), unsupported)), nil, ErrUnsupportedCredentials},
		{"depth", txEnvelope(invokeOp(invokeContract(contractAddress(testContractKey), "f", []xdr.ScVal{vecVal(vecVal())}))), []Option{WithMaxDepth(2)}, ErrDepthLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ExplainEnvelope(tc.env, tc.opts...)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if !strings.HasPrefix(err.Error(), "explain: explain envelope: ") {
				t.Errorf("err = %q, not wrapped", err)
			}
		})
	}
	if _, err := ExplainEnvelope(txEnvelope(), WithMaxNodes(0)); err == nil {
		t.Error("max nodes 0 accepted")
	}
}
