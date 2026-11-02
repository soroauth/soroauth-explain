package explain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// ActionUploadWasm is a host function that uploads contract code
// (HOST_FUNCTION_TYPE_UPLOAD_CONTRACT_WASM). It appears only as a
// transaction's own invocation, never inside an authorization entry.
const ActionUploadWasm ActionKind = "upload_wasm"

// EnvelopeExplanation is an account of what a transaction envelope's
// operations invoke and what its authorization entries authorize.
//
// Transaction holds the envelope's own fields as read from the bytes: its
// source account, fee, sequence number, memo and preconditions. Operations
// holds one OperationExplanation per operation, in order. Confidence is the
// floor across every operation, and Unexplained gathers every sentence about
// what could not be determined, each prefixed with the operation (and entry)
// it belongs to, so it is the one list a wallet needs for its warning.
//
// It does not show a Soroban transaction's resource limits or footprint, and
// it does not say what the transaction will do when applied. It says what the
// operations invoke and what the entries authorize.
type EnvelopeExplanation struct {
	Confidence  Confidence             `json:"confidence"`
	Transaction []Field                `json:"transaction"`
	Operations  []OperationExplanation `json:"operations"`
	Unexplained []string               `json:"unexplained,omitempty"`
}

// OperationExplanation is one operation of a transaction.
//
// For an InvokeHostFunction operation, Invocation is the host function the
// transaction itself invokes, explained by the same rules as an entry's
// invocation, and Entries explains each authorization entry the operation
// carries. Any other operation type is named and marked opaque: this library
// explains Soroban invocations and authorizations, and an operation left
// uninterpreted is never left out.
type OperationExplanation struct {
	Index      int           `json:"index"`
	Type       string        `json:"type"`
	Source     string        `json:"source,omitempty"`
	Confidence Confidence    `json:"confidence"`
	Invocation *Action       `json:"invocation,omitempty"`
	Entries    []Explanation `json:"entries,omitempty"`
}

// ExplainEnvelope explains every operation and authorization entry in a
// transaction envelope.
//
// The envelope is walked here, not by soroauth-go: the envelope functions in
// that library are newer than the pinned v0.1.0. Each entry is still
// explained through soroauth.Inspect, exactly as Explain does it. The depth
// limit applies to each invocation and entry, and the node limit to the whole
// envelope, so a transaction cannot multiply its budget by carrying many
// entries.
//
// It never modifies env, does not reach the network, and so takes no
// context.
func ExplainEnvelope(env xdr.TransactionEnvelope, opts ...Option) (EnvelopeExplanation, error) {
	o, err := resolveOptions(opts)
	if err != nil {
		return EnvelopeExplanation{}, fmt.Errorf("explain: explain envelope: %w", err)
	}
	tx, ops, err := envelopeTransaction(env)
	if err != nil {
		return EnvelopeExplanation{}, fmt.Errorf("explain: explain envelope: %w", err)
	}

	out := EnvelopeExplanation{
		Confidence:  ConfidenceDecoded,
		Transaction: tx,
		Operations:  []OperationExplanation{},
	}
	if len(ops) == 0 {
		out.Confidence = ConfidenceOpaque
		out.Unexplained = []string{"The transaction has no operations."}
		return out, nil
	}

	w := newWalker(o.maxDepth, o.maxNodes)
	for i := range ops {
		op, notes, err := w.explainOperation(i, ops[i], o)
		if err != nil {
			return EnvelopeExplanation{}, fmt.Errorf("explain: explain envelope: operation %d: %w", i, err)
		}
		out.Operations = append(out.Operations, op)
		out.Confidence = Floor(out.Confidence, op.Confidence)
		out.Unexplained = append(out.Unexplained, notes...)
	}
	return out, nil
}

// envelopeTransaction reads the envelope's own fields and its operations.
// For a fee bump, the outer fee source and fee come first, then the inner
// transaction's fields.
func envelopeTransaction(env xdr.TransactionEnvelope) ([]Field, []xdr.Operation, error) {
	decoded := func(name, value string) Field {
		return Field{Name: name, Value: value, Confidence: ConfidenceDecoded}
	}
	var fields []Field
	switch env.Type {
	case xdr.EnvelopeTypeEnvelopeTypeTxV0:
		if env.V0 == nil {
			return nil, nil, emptyArm("envelope v0")
		}
		tx := env.V0.Tx
		src := xdr.MuxedAccount{Type: xdr.CryptoKeyTypeKeyTypeEd25519, Ed25519: &tx.SourceAccountEd25519}
		addr, err := muxedAddress(src)
		if err != nil {
			return nil, nil, err
		}
		fields = append(fields,
			decoded("envelope", "transaction (v0)"),
			decoded("source", addr),
			decoded("fee", strconv.FormatUint(uint64(tx.Fee), 10)+" stroops"),
			decoded("sequence", strconv.FormatInt(int64(tx.SeqNum), 10)),
		)
		memo, err := memoFields(tx.Memo)
		if err != nil {
			return nil, nil, err
		}
		fields = append(fields, memo...)
		if tx.TimeBounds != nil {
			fields = append(fields, timeBoundsField(*tx.TimeBounds))
		}
		return fields, tx.Operations, nil

	case xdr.EnvelopeTypeEnvelopeTypeTx:
		if env.V1 == nil {
			return nil, nil, emptyArm("envelope v1")
		}
		fields = append(fields, decoded("envelope", "transaction"))
		inner, err := transactionFields(env.V1.Tx)
		if err != nil {
			return nil, nil, err
		}
		return append(fields, inner...), env.V1.Tx.Operations, nil

	case xdr.EnvelopeTypeEnvelopeTypeTxFeeBump:
		if env.FeeBump == nil {
			return nil, nil, emptyArm("envelope fee bump")
		}
		fb := env.FeeBump.Tx
		if fb.InnerTx.Type != xdr.EnvelopeTypeEnvelopeTypeTx || fb.InnerTx.V1 == nil {
			return nil, nil, emptyArm("fee bump inner transaction")
		}
		feeSource, err := muxedAddress(fb.FeeSource)
		if err != nil {
			return nil, nil, err
		}
		fields = append(fields,
			decoded("envelope", "fee bump transaction"),
			decoded("fee_source", feeSource),
			decoded("fee_bump_fee", strconv.FormatInt(int64(fb.Fee), 10)+" stroops"),
		)
		inner, err := transactionFields(fb.InnerTx.V1.Tx)
		if err != nil {
			return nil, nil, err
		}
		return append(fields, inner...), fb.InnerTx.V1.Tx.Operations, nil
	}
	return nil, nil, fmt.Errorf("unknown envelope type %d", int32(env.Type))
}

// transactionFields reads a v1 transaction's source, fee, sequence number,
// memo and preconditions.
func transactionFields(tx xdr.Transaction) ([]Field, error) {
	src, err := muxedAddress(tx.SourceAccount)
	if err != nil {
		return nil, err
	}
	fields := []Field{
		{Name: "source", Value: src, Confidence: ConfidenceDecoded},
		{Name: "fee", Value: strconv.FormatUint(uint64(tx.Fee), 10) + " stroops", Confidence: ConfidenceDecoded},
		{Name: "sequence", Value: strconv.FormatInt(int64(tx.SeqNum), 10), Confidence: ConfidenceDecoded},
	}
	memo, err := memoFields(tx.Memo)
	if err != nil {
		return nil, err
	}
	fields = append(fields, memo...)
	cond, err := preconditionFields(tx.Cond)
	if err != nil {
		return nil, err
	}
	return append(fields, cond...), nil
}

// memoFields renders a memo. Text is quoted, so a control character in it
// cannot reach a terminal; hashes are hex. No memo is no field.
func memoFields(m xdr.Memo) ([]Field, error) {
	f := Field{Name: "memo", Confidence: ConfidenceDecoded}
	switch m.Type {
	case xdr.MemoTypeMemoNone:
		return nil, nil
	case xdr.MemoTypeMemoText:
		if m.Text == nil {
			return nil, emptyArm("memo text")
		}
		f.Value = "text " + strconv.Quote(*m.Text)
	case xdr.MemoTypeMemoId:
		if m.Id == nil {
			return nil, emptyArm("memo id")
		}
		f.Value = "id " + strconv.FormatUint(uint64(*m.Id), 10)
	case xdr.MemoTypeMemoHash:
		if m.Hash == nil {
			return nil, emptyArm("memo hash")
		}
		f.Value = "hash " + hex.EncodeToString(m.Hash[:])
	case xdr.MemoTypeMemoReturn:
		if m.RetHash == nil {
			return nil, emptyArm("memo return")
		}
		f.Value = "return " + hex.EncodeToString(m.RetHash[:])
	default:
		return nil, fmt.Errorf("unknown memo type %d", int32(m.Type))
	}
	return []Field{f}, nil
}

// timeBoundsField renders time bounds as the raw Unix seconds in the bytes.
// They are not converted to dates: that would need a time zone, and output
// must not depend on the environment. Zero is shown as written; what a zero
// bound means is the protocol's business, not stated here.
func timeBoundsField(tb xdr.TimeBounds) Field {
	return Field{
		Name:       "time_bounds",
		Value:      strconv.FormatUint(uint64(tb.MinTime), 10) + ".." + strconv.FormatUint(uint64(tb.MaxTime), 10) + " (unix seconds)",
		Confidence: ConfidenceDecoded,
	}
}

// preconditionFields renders every precondition the transaction sets.
func preconditionFields(c xdr.Preconditions) ([]Field, error) {
	switch c.Type {
	case xdr.PreconditionTypePrecondNone:
		return nil, nil
	case xdr.PreconditionTypePrecondTime:
		if c.TimeBounds == nil {
			return nil, emptyArm("precondition time bounds")
		}
		return []Field{timeBoundsField(*c.TimeBounds)}, nil
	case xdr.PreconditionTypePrecondV2:
		if c.V2 == nil {
			return nil, emptyArm("precondition v2")
		}
		v2 := c.V2
		var fields []Field
		if v2.TimeBounds != nil {
			fields = append(fields, timeBoundsField(*v2.TimeBounds))
		}
		if v2.LedgerBounds != nil {
			fields = append(fields, Field{Name: "ledger_bounds",
				Value:      strconv.FormatUint(uint64(v2.LedgerBounds.MinLedger), 10) + ".." + strconv.FormatUint(uint64(v2.LedgerBounds.MaxLedger), 10),
				Confidence: ConfidenceDecoded})
		}
		if v2.MinSeqNum != nil {
			fields = append(fields, Field{Name: "min_sequence", Value: strconv.FormatInt(int64(*v2.MinSeqNum), 10), Confidence: ConfidenceDecoded})
		}
		if v2.MinSeqAge != 0 {
			fields = append(fields, Field{Name: "min_sequence_age", Value: strconv.FormatUint(uint64(v2.MinSeqAge), 10) + " seconds", Confidence: ConfidenceDecoded})
		}
		if v2.MinSeqLedgerGap != 0 {
			fields = append(fields, Field{Name: "min_sequence_ledger_gap", Value: strconv.FormatUint(uint64(v2.MinSeqLedgerGap), 10), Confidence: ConfidenceDecoded})
		}
		for i := range v2.ExtraSigners {
			addr, err := v2.ExtraSigners[i].GetAddress()
			if err != nil {
				return nil, fmt.Errorf("extra signer %d: %w", i, err)
			}
			fields = append(fields, Field{Name: "extra_signer[" + strconv.Itoa(i) + "]", Value: addr, Confidence: ConfidenceDecoded})
		}
		return fields, nil
	}
	return nil, fmt.Errorf("unknown precondition type %d", int32(c.Type))
}

func muxedAddress(m xdr.MuxedAccount) (string, error) {
	addr, err := m.GetAddress()
	if err != nil {
		return "", fmt.Errorf("format account: %w", err)
	}
	return addr, nil
}

// operationTypeName is the operation type without the SDK's Go prefix:
// "InvokeHostFunction", "Payment". An unknown type is named by its number.
func operationTypeName(t xdr.OperationType) string {
	if name := strings.TrimPrefix(t.String(), "OperationType"); name != "" {
		return name
	}
	return "unknown operation type " + strconv.Itoa(int(t))
}

// explainOperation explains one operation and returns its notes, prefixed
// with where they come from.
func (w *walker) explainOperation(index int, op xdr.Operation, o options) (OperationExplanation, []string, error) {
	out := OperationExplanation{
		Index:      index,
		Type:       operationTypeName(op.Body.Type),
		Confidence: ConfidenceDecoded,
	}
	if op.SourceAccount != nil {
		src, err := muxedAddress(*op.SourceAccount)
		if err != nil {
			return OperationExplanation{}, nil, err
		}
		out.Source = src
	}
	prefix := "Operation " + strconv.Itoa(index) + ": "

	if op.Body.Type != xdr.OperationTypeInvokeHostFunction {
		out.Confidence = ConfidenceOpaque
		note := prefix + "a " + out.Type + " operation, which this library does not interpret."
		switch op.Body.Type {
		case xdr.OperationTypeExtendFootprintTtl, xdr.OperationTypeRestoreFootprint:
			note = prefix + "a " + out.Type + " operation, which acts on the transaction's footprint; this library does not show the footprint."
		}
		return out, []string{note}, nil
	}
	if op.Body.InvokeHostFunctionOp == nil {
		return OperationExplanation{}, nil, emptyArm("invoke host function op")
	}
	ihf := op.Body.InvokeHostFunctionOp

	inv, notes, err := w.explainHostFunction(ihf.HostFunction, o)
	if err != nil {
		return OperationExplanation{}, nil, err
	}
	out.Invocation = &inv
	out.Confidence = Floor(out.Confidence, inv.Confidence)
	var all []string
	for _, n := range notes {
		all = append(all, prefix+"invocation: "+n)
	}

	for i := range ihf.Auth {
		exp, err := explainEntry(ihf.Auth[i], o, w)
		if err != nil {
			return OperationExplanation{}, nil, fmt.Errorf("entry %d: %w", i, err)
		}
		out.Entries = append(out.Entries, exp)
		out.Confidence = Floor(out.Confidence, exp.Confidence)
		for _, n := range exp.Unexplained {
			all = append(all, prefix+"entry "+strconv.Itoa(i)+": "+n)
		}
	}
	return out, all, nil
}

// explainHostFunction explains the host function a transaction invokes, by
// the same rules as an authorized invocation. Only an upload has no
// counterpart in an entry: its code is described by its size and SHA-256
// hash, which is derived here from the bytes.
func (w *walker) explainHostFunction(hf xdr.HostFunction, o options) (Action, []string, error) {
	const depth = 1
	if err := w.visit(depth); err != nil {
		return Action{}, nil, err
	}
	switch hf.Type {
	case xdr.HostFunctionTypeHostFunctionTypeInvokeContract:
		if hf.InvokeContract == nil {
			return Action{}, nil, emptyArm("invoke_contract")
		}
		return w.explainContractFn(*hf.InvokeContract, depth, o)
	case xdr.HostFunctionTypeHostFunctionTypeCreateContract:
		if hf.CreateContract == nil {
			return Action{}, nil, emptyArm("create_contract")
		}
		return w.explainCreateContract(hf.CreateContract.ContractIdPreimage, hf.CreateContract.Executable, nil, depth)
	case xdr.HostFunctionTypeHostFunctionTypeCreateContractV2:
		if hf.CreateContractV2 == nil {
			return Action{}, nil, emptyArm("create_contract_v2")
		}
		args := hf.CreateContractV2.ConstructorArgs
		if args == nil {
			args = []xdr.ScVal{}
		}
		return w.explainCreateContract(hf.CreateContractV2.ContractIdPreimage, hf.CreateContractV2.Executable, args, depth)
	case xdr.HostFunctionTypeHostFunctionTypeUploadContractWasm:
		if hf.Wasm == nil {
			return Action{}, nil, emptyArm("upload_contract_wasm")
		}
		sum := sha256.Sum256(*hf.Wasm)
		fields := []Field{
			{Name: "size", Value: strconv.Itoa(len(*hf.Wasm)), Confidence: ConfidenceDecoded},
			{Name: "sha256", Value: hex.EncodeToString(sum[:]), Confidence: ConfidenceDecoded},
		}
		return Action{
			Kind:       ActionUploadWasm,
			Confidence: ConfidenceDecoded,
			Summary:    summarize("Upload {size} bytes of contract code with hash {sha256}", fields),
			Fields:     fields,
		}, nil, nil
	}
	return Action{}, nil, fmt.Errorf("unknown host function type %d", int32(hf.Type))
}
