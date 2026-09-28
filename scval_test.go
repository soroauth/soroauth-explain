package explain

import (
	"errors"
	"math"
	"strings"
	"testing"

	soroauth "github.com/soroauth/soroauth-go"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// Fixed test addresses. The account is the strkey of 32 bytes of 0x01; the
// contract is the strkey of 32 bytes of 0x02. Both are checked by round-trip
// in TestRenderScValAddress rather than trusted.
var (
	testAccountKey  = [32]byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
	testContractKey = [32]byte{2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2}
)

func accountAddress(key [32]byte) xdr.ScAddress {
	k := xdr.Uint256(key)
	return xdr.ScAddress{
		Type:      xdr.ScAddressTypeScAddressTypeAccount,
		AccountId: &xdr.AccountId{Type: xdr.PublicKeyTypePublicKeyTypeEd25519, Ed25519: &k},
	}
}

func contractAddress(key [32]byte) xdr.ScAddress {
	id := xdr.ContractId(key)
	return xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &id}
}

func ptr[T any](v T) *T { return &v }

func vecVal(elems ...xdr.ScVal) xdr.ScVal {
	v := xdr.ScVec(elems)
	p := &v
	return xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &p}
}

func mapVal(entries ...xdr.ScMapEntry) xdr.ScVal {
	m := xdr.ScMap(entries)
	p := &m
	return xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &p}
}

func symVal(s string) xdr.ScVal {
	sym := xdr.ScSymbol(s)
	return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
}

func u32Val(n uint32) xdr.ScVal {
	u := xdr.Uint32(n)
	return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &u}
}

func render(t testing.TB, v xdr.ScVal) value {
	t.Helper()
	got, err := newWalker(DefaultMaxDepth, DefaultMaxNodes).renderScVal(v, 1)
	if err != nil {
		t.Fatalf("renderScVal: %v", err)
	}
	return got
}

func TestRenderScValEveryType(t *testing.T) {
	var nilVec *xdr.ScVec
	var nilMap *xdr.ScMap
	wasm := xdr.Hash{0xab, 0xcd}
	storage := xdr.ScMap{{Key: symVal("k"), Val: u32Val(1)}}

	tests := []struct {
		name     string
		val      xdr.ScVal
		wantText string
		wantConf Confidence
	}{
		{"bool", xdr.ScVal{Type: xdr.ScValTypeScvBool, B: ptr(true)}, "true", ConfidenceDecoded},
		{"void", xdr.ScVal{Type: xdr.ScValTypeScvVoid}, "void", ConfidenceDecoded},
		{"error_contract", xdr.ScVal{Type: xdr.ScValTypeScvError, Error: &xdr.ScError{
			Type: xdr.ScErrorTypeSceContract, ContractCode: ptr(xdr.Uint32(7))}}, "error(contract, 7)", ConfidenceDecoded},
		{"error_budget", xdr.ScVal{Type: xdr.ScValTypeScvError, Error: &xdr.ScError{
			Type: xdr.ScErrorTypeSceBudget, Code: ptr(xdr.ScErrorCodeScecExceededLimit)}}, "error(budget, exceeded_limit)", ConfidenceDecoded},
		{"u32", u32Val(math.MaxUint32), "u32(4294967295)", ConfidenceDecoded},
		{"i32", xdr.ScVal{Type: xdr.ScValTypeScvI32, I32: ptr(xdr.Int32(math.MinInt32))}, "i32(-2147483648)", ConfidenceDecoded},
		{"u64", xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: ptr(xdr.Uint64(math.MaxUint64))}, "u64(18446744073709551615)", ConfidenceDecoded},
		{"i64", xdr.ScVal{Type: xdr.ScValTypeScvI64, I64: ptr(xdr.Int64(math.MinInt64))}, "i64(-9223372036854775808)", ConfidenceDecoded},
		{"timepoint", xdr.ScVal{Type: xdr.ScValTypeScvTimepoint, Timepoint: ptr(xdr.TimePoint(1700000000))}, "timepoint(1700000000)", ConfidenceDecoded},
		{"duration", xdr.ScVal{Type: xdr.ScValTypeScvDuration, Duration: ptr(xdr.Duration(60))}, "duration(60)", ConfidenceDecoded},
		{"u128", xdr.ScVal{Type: xdr.ScValTypeScvU128, U128: &xdr.UInt128Parts{Hi: 1, Lo: 0}}, "u128(18446744073709551616)", ConfidenceDecoded},
		{"i128", xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &xdr.Int128Parts{Hi: 0, Lo: 1000000000}}, "i128(1000000000)", ConfidenceDecoded},
		{"u256", xdr.ScVal{Type: xdr.ScValTypeScvU256, U256: &xdr.UInt256Parts{LoLo: 5}}, "u256(5)", ConfidenceDecoded},
		{"i256", xdr.ScVal{Type: xdr.ScValTypeScvI256, I256: &xdr.Int256Parts{HiHi: -1, HiLo: math.MaxUint64, LoHi: math.MaxUint64, LoLo: math.MaxUint64}}, "i256(-1)", ConfidenceDecoded},
		{"bytes", xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: ptr(xdr.ScBytes{0xde, 0xad})}, "bytes[2](dead)", ConfidenceDecoded},
		{"bytes_empty", xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: ptr(xdr.ScBytes{})}, "bytes[0]()", ConfidenceDecoded},
		{"string", xdr.ScVal{Type: xdr.ScValTypeScvString, Str: ptr(xdr.ScString("hi"))}, `"hi"`, ConfidenceDecoded},
		{"string_escape", xdr.ScVal{Type: xdr.ScValTypeScvString, Str: ptr(xdr.ScString("a\nb\x1b[31m"))}, `"a\nb\x1b[31m"`, ConfidenceDecoded},
		{"symbol", symVal("transfer"), "sym(transfer)", ConfidenceDecoded},
		{"symbol_unplain", symVal("a b"), `sym("a b")`, ConfidenceOpaque},
		{"vec", vecVal(u32Val(1), symVal("x")), "[u32(1), sym(x)]", ConfidenceDecoded},
		{"vec_empty", vecVal(), "[]", ConfidenceDecoded},
		{"vec_absent", xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &nilVec}, "vec(absent)", ConfidenceOpaque},
		{"map", mapVal(xdr.ScMapEntry{Key: symVal("b"), Val: u32Val(2)}, xdr.ScMapEntry{Key: symVal("a"), Val: u32Val(1)}),
			"{sym(b): u32(2), sym(a): u32(1)}", ConfidenceDecoded},
		{"map_absent", xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &nilMap}, "map(absent)", ConfidenceOpaque},
		{"address_contract", xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: ptr(contractAddress(testContractKey))},
			"CABAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAFNSZ", ConfidenceDecoded},
		{"address_muxed", xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &xdr.ScAddress{
			Type: xdr.ScAddressTypeScAddressTypeMuxedAccount, MuxedAccount: &xdr.MuxedEd25519Account{Id: 9}}},
			"address(muxed_account)", ConfidenceOpaque},
		{"instance_wasm", xdr.ScVal{Type: xdr.ScValTypeScvContractInstance, Instance: &xdr.ScContractInstance{
			Executable: xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableWasm, WasmHash: &wasm},
			Storage:    &storage}},
			"contract_instance(wasm abcd000000000000000000000000000000000000000000000000000000000000, storage {sym(k): u32(1)})", ConfidenceDecoded},
		{"instance_sac", xdr.ScVal{Type: xdr.ScValTypeScvContractInstance, Instance: &xdr.ScContractInstance{
			Executable: xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableStellarAsset}}},
			"contract_instance(stellar_asset, storage absent)", ConfidenceDecoded},
		{"instance_external", xdr.ScVal{Type: xdr.ScValTypeScvContractInstance, Instance: &xdr.ScContractInstance{
			Executable: xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableExternalRef}}},
			"contract_instance(external_ref, storage absent)", ConfidenceOpaque},
		{"ledger_key_contract_instance", xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance}, "ledger_key_contract_instance", ConfidenceDecoded},
		{"ledger_key_nonce", xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyNonce, NonceKey: &xdr.ScNonceKey{Nonce: -3}}, "ledger_key_nonce(-3)", ConfidenceDecoded},
		{"executable_tag", xdr.ScVal{Type: xdr.ScValTypeScvExecutableTag, ExecutableTag: ptr(xdr.ScString("t"))}, `executable_tag("t")`, ConfidenceOpaque},
		{"unknown_type", xdr.ScVal{Type: xdr.ScValType(99)}, "unknown_scval_type(99)", ConfidenceOpaque},
	}

	covered := map[xdr.ScValType]bool{}
	for _, tt := range tests {
		covered[tt.val.Type] = true
		t.Run(tt.name, func(t *testing.T) {
			got := render(t, tt.val)
			if got.text != tt.wantText {
				t.Errorf("text = %s, want %s", got.text, tt.wantText)
			}
			if got.conf != tt.wantConf {
				t.Errorf("confidence = %s, want %s", got.conf, tt.wantConf)
			}
			if got.conf != ConfidenceDecoded && len(got.notes) == 0 {
				t.Errorf("confidence %s with no note explaining why", got.conf)
			}
			if got.conf == ConfidenceDecoded && len(got.notes) != 0 {
				t.Errorf("decoded value carries notes: %q", got.notes)
			}
		})
	}

	// Every ScValType the SDK defines must have a case above. This fails
	// when an SDK upgrade adds a type, which is when a new case is needed.
	var probe xdr.ScValType
	for i := int32(0); i < 256; i++ {
		if probe.ValidEnum(i) && !covered[xdr.ScValType(i)] {
			t.Errorf("ScValType %d (%s) has no test case", i, xdr.ScValType(i))
		}
	}
}

func TestRenderScValAddress(t *testing.T) {
	// Round-trip: the rendered strkey parses back to the same bytes, so the
	// fixed strings in the table above are derived, not trusted.
	for _, a := range []xdr.ScAddress{accountAddress(testAccountKey), contractAddress(testContractKey)} {
		got := render(t, xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &a})
		if got.conf != ConfidenceDecoded {
			t.Fatalf("confidence = %s", got.conf)
		}
		back, err := soroauth.ParseAddress(got.text)
		if err != nil {
			t.Fatalf("ParseAddress(%s): %v", got.text, err)
		}
		if !a.Equals(back) {
			t.Fatalf("%s did not round-trip", got.text)
		}
	}
}

func TestRenderInt128Boundaries(t *testing.T) {
	tests := []struct {
		name string
		p    xdr.Int128Parts
		want string
	}{
		{"zero", xdr.Int128Parts{Hi: 0, Lo: 0}, "0"},
		{"one", xdr.Int128Parts{Hi: 0, Lo: 1}, "1"},
		{"2^63-1", xdr.Int128Parts{Hi: 0, Lo: math.MaxInt64}, "9223372036854775807"},
		{"2^64", xdr.Int128Parts{Hi: 1, Lo: 0}, "18446744073709551616"},
		{"max", xdr.Int128Parts{Hi: math.MaxInt64, Lo: math.MaxUint64}, "170141183460469231731687303715884105727"},
		{"min", xdr.Int128Parts{Hi: math.MinInt64, Lo: 0}, "-170141183460469231731687303715884105728"},
		{"minus_one", xdr.Int128Parts{Hi: -1, Lo: math.MaxUint64}, "-1"},
		{"minus_2^64", xdr.Int128Parts{Hi: -1, Lo: 0}, "-18446744073709551616"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := i128String(tt.p); got != tt.want {
				t.Fatalf("i128String = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestRenderUint128Boundaries(t *testing.T) {
	tests := []struct {
		name string
		p    xdr.UInt128Parts
		want string
	}{
		{"zero", xdr.UInt128Parts{}, "0"},
		{"one", xdr.UInt128Parts{Lo: 1}, "1"},
		{"2^63-1", xdr.UInt128Parts{Lo: math.MaxInt64}, "9223372036854775807"},
		{"2^64", xdr.UInt128Parts{Hi: 1}, "18446744073709551616"},
		{"max", xdr.UInt128Parts{Hi: math.MaxUint64, Lo: math.MaxUint64}, "340282366920938463463374607431768211455"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := u128String(tt.p); got != tt.want {
				t.Fatalf("u128String = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestRender256Boundaries(t *testing.T) {
	if got, want := u256String(xdr.UInt256Parts{HiHi: math.MaxUint64, HiLo: math.MaxUint64, LoHi: math.MaxUint64, LoLo: math.MaxUint64}),
		"115792089237316195423570985008687907853269984665640564039457584007913129639935"; got != want {
		t.Errorf("u256 max = %s, want %s", got, want)
	}
	if got, want := i256String(xdr.Int256Parts{HiHi: math.MinInt64}),
		"-57896044618658097711785492504343953926634992332820282019728792003956564819968"; got != want {
		t.Errorf("i256 min = %s, want %s", got, want)
	}
	if got, want := i256String(xdr.Int256Parts{LoHi: 1}), "18446744073709551616"; got != want {
		t.Errorf("i256 2^64 = %s, want %s", got, want)
	}
}

func nested(depth int) xdr.ScVal {
	v := u32Val(0)
	for i := 1; i < depth; i++ {
		v = vecVal(v)
	}
	return v
}

func TestRenderScValDepthLimit(t *testing.T) {
	// nested(n) has n levels. Rendered from depth 1, n levels fit a limit of n.
	if _, err := newWalker(5, DefaultMaxNodes).renderScVal(nested(5), 1); err != nil {
		t.Fatalf("depth 5 under limit 5: %v", err)
	}
	_, err := newWalker(5, DefaultMaxNodes).renderScVal(nested(6), 1)
	if !errors.Is(err, ErrDepthLimit) {
		t.Fatalf("depth 6 under limit 5: err = %v, want ErrDepthLimit", err)
	}
	// Far past the SDK's own decode depth: still an error, never a panic.
	_, err = newWalker(DefaultMaxDepth, DefaultMaxNodes).renderScVal(nested(5000), 1)
	if !errors.Is(err, ErrDepthLimit) {
		t.Fatalf("depth 5000: err = %v, want ErrDepthLimit", err)
	}
}

func TestRenderScValNodeLimit(t *testing.T) {
	elems := make([]xdr.ScVal, 9)
	for i := range elems {
		elems[i] = u32Val(uint32(i))
	}
	// One vec plus nine elements is ten nodes.
	if _, err := newWalker(DefaultMaxDepth, 10).renderScVal(vecVal(elems...), 1); err != nil {
		t.Fatalf("10 nodes under limit 10: %v", err)
	}
	_, err := newWalker(DefaultMaxDepth, 9).renderScVal(vecVal(elems...), 1)
	if !errors.Is(err, ErrNodeLimit) {
		t.Fatalf("10 nodes under limit 9: err = %v, want ErrNodeLimit", err)
	}
}

func TestRenderScValEmptyArm(t *testing.T) {
	tests := []xdr.ScVal{
		{Type: xdr.ScValTypeScvBool},
		{Type: xdr.ScValTypeScvI128},
		{Type: xdr.ScValTypeScvVec},
		{Type: xdr.ScValTypeScvMap},
		{Type: xdr.ScValTypeScvAddress},
		{Type: xdr.ScValTypeScvError, Error: &xdr.ScError{Type: xdr.ScErrorTypeSceContract}},
		{Type: xdr.ScValTypeScvContractInstance, Instance: &xdr.ScContractInstance{
			Executable: xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableWasm}}},
	}
	for _, v := range tests {
		t.Run(v.Type.String(), func(t *testing.T) {
			_, err := newWalker(DefaultMaxDepth, DefaultMaxNodes).renderScVal(v, 1)
			if !errors.Is(err, ErrEmptyArm) {
				t.Fatalf("err = %v, want ErrEmptyArm", err)
			}
		})
	}
}

func TestRenderScValOpaqueFloors(t *testing.T) {
	// One opaque element makes the whole container opaque, and its note
	// survives to the top.
	got := render(t, vecVal(u32Val(1), mapVal(xdr.ScMapEntry{Key: symVal("ok"), Val: symVal("not ok")})))
	if got.conf != ConfidenceOpaque {
		t.Fatalf("confidence = %s, want opaque", got.conf)
	}
	if len(got.notes) != 1 || !strings.Contains(got.notes[0], "symbol") {
		t.Fatalf("notes = %q", got.notes)
	}
}
