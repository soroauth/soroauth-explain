package spec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// fixture is the provenance of a recorded wasm module.
type fixture struct {
	Contract   string `json:"contract"`
	WasmFile   string `json:"wasm_file"`
	WasmSHA256 string `json:"wasm_sha256"`
}

func loadOracle(t testing.TB) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/oracle_testnet.json")
	if err != nil {
		t.Fatal(err)
	}
	var fx fixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	wasm, err := os.ReadFile("testdata/" + fx.WasmFile)
	if err != nil {
		t.Fatal(err)
	}
	// Soroban identifies contract code by its SHA-256, so matching the hash
	// the network stores proves the fixture is the on-chain code.
	sum := sha256.Sum256(wasm)
	if hex.EncodeToString(sum[:]) != fx.WasmSHA256 {
		t.Fatalf("fixture sha256 %x, network wasm hash %s", sum, fx.WasmSHA256)
	}
	return wasm
}

func TestFromWasmRealContract(t *testing.T) {
	s, err := FromWasm(loadOracle(t))
	if err != nil {
		t.Fatal(err)
	}
	if s.Len() != 10 {
		t.Errorf("%d functions, want 10", s.Len())
	}
	f, ok := s.Function("set_price")
	if !ok {
		t.Fatal("set_price not found")
	}
	want := []struct {
		name string
		typ  xdr.ScSpecType
	}{
		{"caller", xdr.ScSpecTypeScSpecTypeAddress},
		{"symbol", xdr.ScSpecTypeScSpecTypeSymbol},
		{"price", xdr.ScSpecTypeScSpecTypeI128},
	}
	if len(f.Inputs) != len(want) {
		t.Fatalf("set_price inputs %+v", f.Inputs)
	}
	for i, w := range want {
		if f.Inputs[i].Name != w.name || f.Inputs[i].Type.Type != w.typ {
			t.Errorf("input %d = %s %s, want %s %s", i, f.Inputs[i].Name, f.Inputs[i].Type.Type, w.name, w.typ)
		}
	}
}

// module builds a wasm module from sections: each is an id and contents.
func module(sections ...[]byte) []byte {
	b := []byte("\x00asm\x01\x00\x00\x00")
	for _, s := range sections {
		b = append(b, s...)
	}
	return b
}

func leb(n int) []byte {
	var out []byte
	for {
		c := byte(n & 0x7f)
		n >>= 7
		if n != 0 {
			out = append(out, c|0x80)
			continue
		}
		return append(out, c)
	}
}

func custom(name string, data []byte) []byte {
	body := append(append(leb(len(name)), name...), data...)
	return append(append([]byte{0}, leb(len(body))...), body...)
}

func specBytes(t testing.TB, entries ...xdr.ScSpecEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, e := range entries {
		b, err := e.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(b)
	}
	return buf.Bytes()
}

func fn(name string, inputs ...xdr.ScSpecFunctionInputV0) xdr.ScSpecEntry {
	return xdr.ScSpecEntry{Kind: xdr.ScSpecEntryKindScSpecEntryFunctionV0, FunctionV0: &xdr.ScSpecFunctionV0{
		Name: xdr.ScSymbol(name), Inputs: inputs,
	}}
}

func in(name string, typ xdr.ScSpecType) xdr.ScSpecFunctionInputV0 {
	return xdr.ScSpecFunctionInputV0{Name: name, Type: xdr.ScSpecTypeDef{Type: typ}}
}

func TestFromWasmBuilt(t *testing.T) {
	spec := specBytes(t, fn("f", in("a", xdr.ScSpecTypeScSpecTypeU32)))
	// Another custom section first, a non-custom section, then the spec.
	s, err := FromWasm(module(custom("other", []byte{1, 2, 3}), []byte{1, 1, 0}, custom(SectionName, spec)))
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := s.Function("f"); !ok || f.Inputs[0].Name != "a" {
		t.Fatalf("got %+v", f)
	}
	// The first spec section wins, as in rs-soroban-sdk.
	s, err = FromWasm(module(custom(SectionName, spec), custom(SectionName, specBytes(t, fn("g")))))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Function("f"); !ok {
		t.Fatal("first spec section not used")
	}
}

func TestFromWasmHostile(t *testing.T) {
	tests := []struct {
		name string
		wasm []byte
		want error
	}{
		{"empty", nil, ErrMalformed},
		{"bad_magic", []byte("\x00ASM\x01\x00\x00\x00"), ErrMalformed},
		{"no_sections", module(), ErrNoSpec},
		{"no_spec", module(custom("contractmetav0", []byte{1})), ErrNoSpec},
		{"truncated_leb", module([]byte{0, 0x80}), ErrMalformed},
		{"leb_too_long", module([]byte{0, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01}), ErrMalformed},
		{"leb_overflow", module([]byte{0, 0xff, 0xff, 0xff, 0xff, 0x7f}), ErrMalformed},
		{"section_past_end", module([]byte{0, 10, 1}), ErrMalformed},
		{"name_past_section", module([]byte{0, 2, 9, 'a'}), ErrMalformed},
		{"oversize", append(module(), make([]byte, MaxWasmBytes)...), ErrMalformed},
		{"garbage_spec", module(custom(SectionName, []byte{0, 0, 0, 9, 1, 2})), ErrMalformed},
		{"truncated_spec", module(custom(SectionName, specBytes(t, fn("f", in("a", xdr.ScSpecTypeScSpecTypeU32)))[:10])), ErrMalformed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := FromWasm(tt.wasm)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestParseDepthLimit(t *testing.T) {
	// An option of an option ... 200 deep is past maxSpecDepth.
	typ := xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeU32}
	for i := 0; i < 200; i++ {
		inner := typ
		typ = xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeOption, Option: &xdr.ScSpecTypeOption{ValueType: inner}}
	}
	e := fn("deep", xdr.ScSpecFunctionInputV0{Name: "a", Type: typ})
	b, err := e.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(b); !errors.Is(err, ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed", err)
	}
}

func TestParseDuplicateDropped(t *testing.T) {
	s, err := Parse(specBytes(t, fn("f"), fn("g"), fn("f", in("x", xdr.ScSpecTypeScSpecTypeU32)), fn("f")))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Function("f"); ok {
		t.Fatal("an ambiguous declaration was kept")
	}
	if _, ok := s.Function("g"); !ok || s.Len() != 1 {
		t.Fatalf("len %d", s.Len())
	}
}

func TestMatches(t *testing.T) {
	u32 := xdr.Uint32(1)
	sym := xdr.ScSymbol("s")
	b4 := xdr.ScBytes{1, 2, 3, 4}
	addr := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeAccount}
	vals := map[string]xdr.ScVal{
		"u32":   {Type: xdr.ScValTypeScvU32, U32: &u32},
		"sym":   {Type: xdr.ScValTypeScvSymbol, Sym: &sym},
		"bytes": {Type: xdr.ScValTypeScvBytes, Bytes: &b4},
		"addr":  {Type: xdr.ScValTypeScvAddress, Address: &addr},
		"void":  {Type: xdr.ScValTypeScvVoid},
	}
	bytesN := func(n uint32) xdr.ScSpecTypeDef {
		return xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeBytesN, BytesN: &xdr.ScSpecTypeBytesN{N: xdr.Uint32(n)}}
	}
	optU32 := xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeOption, Option: &xdr.ScSpecTypeOption{ValueType: xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeU32}}}
	tests := []struct {
		name string
		typ  xdr.ScSpecTypeDef
		val  string
		want bool
	}{
		{"u32_ok", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeU32}, "u32", true},
		{"u32_wrong", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeU32}, "sym", false},
		{"symbol_ok", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeSymbol}, "sym", true},
		{"address_ok", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeAddress}, "addr", true},
		{"bytesn_ok", bytesN(4), "bytes", true},
		{"bytesn_wrong_len", bytesN(32), "bytes", false},
		{"option_void", optU32, "void", true},
		{"option_inner", optU32, "u32", true},
		{"option_wrong", optU32, "sym", false},
		{"val_anything", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeVal}, "sym", true},
		{"result_never", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeResult}, "u32", false},
		{"unknown_type", xdr.ScSpecTypeDef{Type: xdr.ScSpecType(9999)}, "u32", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Function{Name: "f", Inputs: []Input{{Name: "a", Type: tt.typ}}}
			if got := f.Matches([]xdr.ScVal{vals[tt.val]}); got != tt.want {
				t.Fatalf("Matches = %v, want %v", got, tt.want)
			}
		})
	}
	f := Function{Name: "f", Inputs: []Input{{Name: "a", Type: xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeU32}}}}
	if f.Matches(nil) || f.Matches([]xdr.ScVal{vals["u32"], vals["u32"]}) {
		t.Fatal("arity not checked")
	}
}
