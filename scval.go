package explain

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"

	soroauth "github.com/soroauth/soroauth-go"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// DefaultMaxDepth is the deepest nesting Explain will walk, counting each
// invocation level and each vector, map or contract-instance level inside an
// argument.
//
// The input is attacker-controlled and the SDK's own decoder allows nesting to
// 1500 (go-xdr xdr3/decode.go:36, DecodeDefaultMaxDepth). Nothing that deep can
// be reviewed by a person, and an explanation nobody can read is not an
// explanation. Measured on 2026-09-28: the deepest entry in soroauth-go
// v0.1.0's golden vectors reaches depth 4, so 32 leaves eight times headroom.
// Override with WithMaxDepth.
const DefaultMaxDepth = 32

// DefaultMaxNodes is the most invocations plus argument values Explain will
// visit in one entry, including every delegate's walk.
//
// It bounds work and output size for an entry that is shallow but very wide.
// Measured on 2026-09-28: the largest entry in soroauth-go v0.1.0's golden
// vectors has 18 nodes, and a 100-argument call has 101. Override with
// WithMaxNodes.
const DefaultMaxNodes = 1024

// walker carries the depth and node budget across one entry. Every node
// visited, invocation or value, draws from the same budget, so the limit
// bounds the whole entry rather than each argument separately.
type walker struct {
	maxDepth int
	maxNodes int
	nodes    int
}

func newWalker(maxDepth, maxNodes int) *walker {
	return &walker{maxDepth: maxDepth, maxNodes: maxNodes}
}

// visit charges one node at the given depth against the budget.
func (w *walker) visit(depth int) error {
	if depth > w.maxDepth {
		return fmt.Errorf("%w (limit %d)", ErrDepthLimit, w.maxDepth)
	}
	w.nodes++
	if w.nodes > w.maxNodes {
		return fmt.Errorf("%w (limit %d)", ErrNodeLimit, w.maxNodes)
	}
	return nil
}

// value is one rendered ScVal: its text, how much of it was understood, and
// a plain sentence for each part that was not.
type value struct {
	text  string
	conf  Confidence
	notes []string
}

func decodedValue(text string) value {
	return value{text: text, conf: ConfidenceDecoded}
}

func opaqueValue(text, note string) value {
	return value{text: text, conf: ConfidenceOpaque, notes: []string{note}}
}

// renderScVal renders v as text. It never interprets meaning: an i128 is
// shown as an integer, not an amount, and an address as its strkey, not a
// name. Anything it cannot render faithfully is marked opaque with a note.
//
// Every ScValType has an explicit case. There is deliberately no default
// that stringifies an unrecognised type; an unknown discriminant is opaque.
func (w *walker) renderScVal(v xdr.ScVal, depth int) (value, error) {
	if err := w.visit(depth); err != nil {
		return value{}, err
	}

	switch v.Type {
	case xdr.ScValTypeScvBool:
		if v.B == nil {
			return value{}, emptyArm("bool")
		}
		return decodedValue(strconv.FormatBool(*v.B)), nil

	case xdr.ScValTypeScvVoid:
		return decodedValue("void"), nil

	case xdr.ScValTypeScvError:
		if v.Error == nil {
			return value{}, emptyArm("error")
		}
		return renderScError(*v.Error)

	case xdr.ScValTypeScvU32:
		if v.U32 == nil {
			return value{}, emptyArm("u32")
		}
		return decodedValue("u32(" + strconv.FormatUint(uint64(*v.U32), 10) + ")"), nil

	case xdr.ScValTypeScvI32:
		if v.I32 == nil {
			return value{}, emptyArm("i32")
		}
		return decodedValue("i32(" + strconv.FormatInt(int64(*v.I32), 10) + ")"), nil

	case xdr.ScValTypeScvU64:
		if v.U64 == nil {
			return value{}, emptyArm("u64")
		}
		return decodedValue("u64(" + strconv.FormatUint(uint64(*v.U64), 10) + ")"), nil

	case xdr.ScValTypeScvI64:
		if v.I64 == nil {
			return value{}, emptyArm("i64")
		}
		return decodedValue("i64(" + strconv.FormatInt(int64(*v.I64), 10) + ")"), nil

	case xdr.ScValTypeScvTimepoint:
		if v.Timepoint == nil {
			return value{}, emptyArm("timepoint")
		}
		// Rendered as the raw integer. Converting to a date would need a
		// time zone and a calendar, and neither is in the bytes.
		return decodedValue("timepoint(" + strconv.FormatUint(uint64(*v.Timepoint), 10) + ")"), nil

	case xdr.ScValTypeScvDuration:
		if v.Duration == nil {
			return value{}, emptyArm("duration")
		}
		return decodedValue("duration(" + strconv.FormatUint(uint64(*v.Duration), 10) + ")"), nil

	case xdr.ScValTypeScvU128:
		if v.U128 == nil {
			return value{}, emptyArm("u128")
		}
		return decodedValue("u128(" + u128String(*v.U128) + ")"), nil

	case xdr.ScValTypeScvI128:
		if v.I128 == nil {
			return value{}, emptyArm("i128")
		}
		return decodedValue("i128(" + i128String(*v.I128) + ")"), nil

	case xdr.ScValTypeScvU256:
		if v.U256 == nil {
			return value{}, emptyArm("u256")
		}
		return decodedValue("u256(" + u256String(*v.U256) + ")"), nil

	case xdr.ScValTypeScvI256:
		if v.I256 == nil {
			return value{}, emptyArm("i256")
		}
		return decodedValue("i256(" + i256String(*v.I256) + ")"), nil

	case xdr.ScValTypeScvBytes:
		if v.Bytes == nil {
			return value{}, emptyArm("bytes")
		}
		return decodedValue(bytesString(*v.Bytes)), nil

	case xdr.ScValTypeScvString:
		if v.Str == nil {
			return value{}, emptyArm("string")
		}
		// Quoted so that control characters, newlines and terminal escape
		// sequences in contract-supplied text are shown, not executed.
		return decodedValue(strconv.Quote(string(*v.Str))), nil

	case xdr.ScValTypeScvSymbol:
		if v.Sym == nil {
			return value{}, emptyArm("symbol")
		}
		return renderSymbol(string(*v.Sym)), nil

	case xdr.ScValTypeScvVec:
		if v.Vec == nil {
			return value{}, emptyArm("vec")
		}
		if *v.Vec == nil {
			return opaqueValue("vec(absent)", "A vector value is marked absent, which has no defined meaning here."), nil
		}
		return w.renderVec(**v.Vec, depth)

	case xdr.ScValTypeScvMap:
		if v.Map == nil {
			return value{}, emptyArm("map")
		}
		if *v.Map == nil {
			return opaqueValue("map(absent)", "A map value is marked absent, which has no defined meaning here."), nil
		}
		return w.renderMap(**v.Map, depth)

	case xdr.ScValTypeScvAddress:
		if v.Address == nil {
			return value{}, emptyArm("address")
		}
		return renderAddress(*v.Address), nil

	case xdr.ScValTypeScvContractInstance:
		if v.Instance == nil {
			return value{}, emptyArm("contract instance")
		}
		return w.renderInstance(*v.Instance, depth)

	case xdr.ScValTypeScvLedgerKeyContractInstance:
		return decodedValue("ledger_key_contract_instance"), nil

	case xdr.ScValTypeScvLedgerKeyNonce:
		if v.NonceKey == nil {
			return value{}, emptyArm("nonce key")
		}
		return decodedValue("ledger_key_nonce(" + strconv.FormatInt(int64(v.NonceKey.Nonce), 10) + ")"), nil

	case xdr.ScValTypeScvExecutableTag:
		if v.ExecutableTag == nil {
			return value{}, emptyArm("executable tag")
		}
		// The bytes are shown, but this library has not read the protocol
		// text that gives this type its meaning, so it claims nothing.
		return opaqueValue("executable_tag("+strconv.Quote(string(*v.ExecutableTag))+")",
			"An executable-tag value is shown as its raw text; its meaning is not interpreted."), nil
	}

	return opaqueValue(fmt.Sprintf("unknown_scval_type(%d)", int32(v.Type)),
		fmt.Sprintf("A value of unknown type %d cannot be rendered.", int32(v.Type))), nil
}

func emptyArm(arm string) error {
	return fmt.Errorf("%w: %s", ErrEmptyArm, arm)
}

func (w *walker) renderVec(vec xdr.ScVec, depth int) (value, error) {
	out := value{conf: ConfidenceDecoded}
	text := "["
	for i := range vec {
		elem, err := w.renderScVal(vec[i], depth+1)
		if err != nil {
			return value{}, err
		}
		if i > 0 {
			text += ", "
		}
		text += elem.text
		out.merge(elem)
	}
	out.text = text + "]"
	return out, nil
}

// renderMap renders entries in stored order. ScMap is an XDR array, so its
// order comes from the bytes and rendering is deterministic without sorting.
func (w *walker) renderMap(m xdr.ScMap, depth int) (value, error) {
	out := value{conf: ConfidenceDecoded}
	text := "{"
	for i := range m {
		key, err := w.renderScVal(m[i].Key, depth+1)
		if err != nil {
			return value{}, err
		}
		val, err := w.renderScVal(m[i].Val, depth+1)
		if err != nil {
			return value{}, err
		}
		if i > 0 {
			text += ", "
		}
		text += key.text + ": " + val.text
		out.merge(key)
		out.merge(val)
	}
	out.text = text + "}"
	return out, nil
}

func (w *walker) renderInstance(inst xdr.ScContractInstance, depth int) (value, error) {
	exec, err := renderExecutable(inst.Executable)
	if err != nil {
		return value{}, err
	}
	out := value{conf: exec.conf, notes: exec.notes}
	storage := "absent"
	if inst.Storage != nil {
		s, err := w.renderMap(*inst.Storage, depth)
		if err != nil {
			return value{}, err
		}
		storage = s.text
		out.merge(s)
	}
	out.text = "contract_instance(" + exec.text + ", storage " + storage + ")"
	return out, nil
}

// renderExecutable renders the code a contract runs. The Wasm arm names its
// hash; the Stellar Asset arm is protocol-defined and needs nothing more.
func renderExecutable(e xdr.ContractExecutable) (value, error) {
	switch e.Type {
	case xdr.ContractExecutableTypeContractExecutableWasm:
		if e.WasmHash == nil {
			return value{}, emptyArm("wasm hash")
		}
		return decodedValue("wasm " + hex.EncodeToString(e.WasmHash[:])), nil
	case xdr.ContractExecutableTypeContractExecutableStellarAsset:
		return decodedValue("stellar_asset"), nil
	case xdr.ContractExecutableTypeContractExecutableExternalRef:
		return opaqueValue("external_ref",
			"A contract executable is an external reference, which this library does not interpret."), nil
	}
	return opaqueValue(fmt.Sprintf("unknown_executable(%d)", int32(e.Type)),
		fmt.Sprintf("A contract executable of unknown type %d cannot be rendered.", int32(e.Type))), nil
}

// merge folds a child's confidence and notes into v.
func (v *value) merge(child value) {
	v.conf = Floor(v.conf, child.conf)
	v.notes = append(v.notes, child.notes...)
}

// renderAddress renders through soroauth.FormatAddress so this library and
// the signer agree on every address. FormatAddress refuses muxed,
// claimable-balance and liquidity-pool addresses; those are opaque here
// rather than rendered by a second, hand-rolled encoder.
func renderAddress(a xdr.ScAddress) value {
	s, err := soroauth.FormatAddress(a)
	if err != nil {
		name := addressTypeName(a.Type)
		return opaqueValue("address("+name+")",
			"An address of type "+name+" is not rendered.")
	}
	return decodedValue(s)
}

func addressTypeName(t xdr.ScAddressType) string {
	switch t {
	case xdr.ScAddressTypeScAddressTypeAccount:
		return "account"
	case xdr.ScAddressTypeScAddressTypeContract:
		return "contract"
	case xdr.ScAddressTypeScAddressTypeMuxedAccount:
		return "muxed_account"
	case xdr.ScAddressTypeScAddressTypeClaimableBalance:
		return "claimable_balance"
	case xdr.ScAddressTypeScAddressTypeLiquidityPool:
		return "liquidity_pool"
	}
	return fmt.Sprintf("unknown(%d)", int32(t))
}

// isPlainSymbol reports whether s consists only of ASCII letters, digits and
// underscores. It is a display rule, not a protocol claim: anything else is
// shown quoted so that it cannot pass for surrounding text or smuggle
// terminal control sequences into a rendering.
func isPlainSymbol(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

func renderSymbol(s string) value {
	if isPlainSymbol(s) {
		return decodedValue("sym(" + s + ")")
	}
	return opaqueValue("sym("+strconv.Quote(s)+")",
		"A symbol contains characters outside A-Z, a-z, 0-9 and underscore, and is shown quoted.")
}

func renderScError(e xdr.ScError) (value, error) {
	typ, ok := scErrorTypeNames[e.Type]
	if !ok {
		return opaqueValue(fmt.Sprintf("error(unknown_type %d)", int32(e.Type)),
			fmt.Sprintf("An error value has unknown type %d.", int32(e.Type))), nil
	}
	if e.Type == xdr.ScErrorTypeSceContract {
		if e.ContractCode == nil {
			return value{}, emptyArm("error contract code")
		}
		return decodedValue("error(contract, " + strconv.FormatUint(uint64(*e.ContractCode), 10) + ")"), nil
	}
	if e.Code == nil {
		return value{}, emptyArm("error code")
	}
	code, ok := scErrorCodeNames[*e.Code]
	if !ok {
		return opaqueValue(fmt.Sprintf("error(%s, unknown_code %d)", typ, int32(*e.Code)),
			fmt.Sprintf("An error value has unknown code %d.", int32(*e.Code))), nil
	}
	return decodedValue("error(" + typ + ", " + code + ")"), nil
}

// Names follow the XDR enumerators SCE_* and SCEC_* (Stellar-contract.x), as
// generated in go-stellar-sdk xdr/xdr_generated.go.
var scErrorTypeNames = map[xdr.ScErrorType]string{
	xdr.ScErrorTypeSceContract: "contract",
	xdr.ScErrorTypeSceWasmVm:   "wasm_vm",
	xdr.ScErrorTypeSceContext:  "context",
	xdr.ScErrorTypeSceStorage:  "storage",
	xdr.ScErrorTypeSceObject:   "object",
	xdr.ScErrorTypeSceCrypto:   "crypto",
	xdr.ScErrorTypeSceEvents:   "events",
	xdr.ScErrorTypeSceBudget:   "budget",
	xdr.ScErrorTypeSceValue:    "value",
	xdr.ScErrorTypeSceAuth:     "auth",
}

var scErrorCodeNames = map[xdr.ScErrorCode]string{
	xdr.ScErrorCodeScecArithDomain:    "arith_domain",
	xdr.ScErrorCodeScecIndexBounds:    "index_bounds",
	xdr.ScErrorCodeScecInvalidInput:   "invalid_input",
	xdr.ScErrorCodeScecMissingValue:   "missing_value",
	xdr.ScErrorCodeScecExistingValue:  "existing_value",
	xdr.ScErrorCodeScecExceededLimit:  "exceeded_limit",
	xdr.ScErrorCodeScecInvalidAction:  "invalid_action",
	xdr.ScErrorCodeScecInternalError:  "internal_error",
	xdr.ScErrorCodeScecUnexpectedType: "unexpected_type",
	xdr.ScErrorCodeScecUnexpectedSize: "unexpected_size",
}

func bytesString(b []byte) string {
	return "bytes[" + strconv.Itoa(len(b)) + "](" + hex.EncodeToString(b) + ")"
}

// The 128- and 256-bit integers are stored as 64-bit limbs, most significant
// first; the signed forms carry the sign in the top limb as a two's-complement
// int64. They are reassembled exactly with math/big: no float, no overflow.

func u128String(p xdr.UInt128Parts) string {
	return limbs(new(big.Int).SetUint64(uint64(p.Hi)), uint64(p.Lo)).String()
}

func i128String(p xdr.Int128Parts) string {
	return limbs(big.NewInt(int64(p.Hi)), uint64(p.Lo)).String()
}

func u256String(p xdr.UInt256Parts) string {
	return limbs(new(big.Int).SetUint64(uint64(p.HiHi)), uint64(p.HiLo), uint64(p.LoHi), uint64(p.LoLo)).String()
}

func i256String(p xdr.Int256Parts) string {
	return limbs(big.NewInt(int64(p.HiHi)), uint64(p.HiLo), uint64(p.LoHi), uint64(p.LoLo)).String()
}

// limbs returns top·2^(64·len(rest)) + rest, treating each of rest as an
// unsigned 64-bit limb. With a negative top this is exactly the two's-
// complement value.
func limbs(top *big.Int, rest ...uint64) *big.Int {
	n := new(big.Int).Set(top)
	for _, limb := range rest {
		n.Lsh(n, 64)
		n.Add(n, new(big.Int).SetUint64(limb))
	}
	return n
}
