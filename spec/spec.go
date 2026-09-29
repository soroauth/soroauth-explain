// Package spec reads a Soroban contract's own interface specification: the
// names and declared types of its functions' arguments.
//
// A spec names arguments; it does not say what a contract does with them.
// explain uses a spec only to label the arguments of a call it cannot
// otherwise interpret, and a call named this way is at most partial, never
// decoded. Nothing in a spec is used to label a contract, an asset, an
// amount or a decimal scale: the contract wrote its spec, and it can write
// anything.
//
// Parsing is offline and bounded (FromWasm, Parse). Fetching a spec from the
// network is separate (RPC) and takes a context.
package spec

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"
	xdr3 "github.com/stellar/go-xdr/xdr3"
)

// SectionName is the WebAssembly custom section a Soroban contract's spec
// lives in: rs-soroban-sdk soroban-spec/src/read.rs:51, read at
// 5ad47088b1e2aafde62ad9ca01f0148b0ce89a98 on 2026-09-29. Its contents are
// ScSpecEntry XDR values, one after another (read.rs parse_raw).
const SectionName = "contractspecv0"

// MaxWasmBytes bounds the wasm FromWasm will read. Both networks cap contract
// code at 131072 bytes (ConfigSettingContractMaxSizeBytes, read from testnet
// at ledger 4928467 and the public network at ledger 64676028 on
// 2026-09-29); this allows twice that, in case the cap is raised.
const MaxWasmBytes = 256 << 10

// maxSpecDepth bounds XDR nesting while decoding spec entries. Spec types
// nest (an option of a vec of a map, and so on); 64 matches the CLI's entry
// decoding limit and is far beyond anything a real type definition needs.
const maxSpecDepth = 64

// ErrNoSpec is returned when a wasm module has no contract spec section.
var ErrNoSpec = errors.New("no contract spec section")

// ErrMalformed is returned for wasm or spec bytes that cannot be read.
var ErrMalformed = errors.New("malformed wasm or contract spec")

// Input is one declared argument of a spec function.
type Input struct {
	Name string
	Type xdr.ScSpecTypeDef
}

// Function is one function declared in a spec.
type Function struct {
	Name   string
	Inputs []Input
}

// Spec is the function declarations of one contract's spec.
type Spec struct {
	functions map[string]Function
}

// Function returns the declaration of a function. A name declared more
// than once is ambiguous and is not returned.
func (s Spec) Function(name string) (Function, bool) {
	f, ok := s.functions[name]
	return f, ok
}

// Len is the number of usable function declarations.
func (s Spec) Len() int { return len(s.functions) }

// FromWasm finds the contract spec section in a wasm module and parses it.
//
// Only the module preamble and the section headers are read, following the
// WebAssembly binary format: the magic "\0asm", a 4-byte version, then
// sections of a one-byte id, a u32 LEB128 length and that many bytes; a
// custom section (id 0) starts with a name, a u32 LEB128 length and UTF-8
// bytes (WebAssembly/spec document/core/binary/modules.rst and values.rst,
// read at 608711107b7f1edb13efd57b7d79b49477462d36). As rs-soroban-sdk
// does, the first section with SectionName is used.
func FromWasm(wasm []byte) (Spec, error) {
	section, err := Section(wasm)
	if err != nil {
		return Spec{}, err
	}
	return Parse(section)
}

// Section returns the raw contents of the contract spec section.
func Section(wasm []byte) ([]byte, error) {
	if len(wasm) > MaxWasmBytes {
		return nil, fmt.Errorf("spec: wasm is %d bytes, over the %d-byte limit: %w", len(wasm), MaxWasmBytes, ErrMalformed)
	}
	if len(wasm) < 8 || !bytes.Equal(wasm[:4], []byte("\x00asm")) {
		return nil, fmt.Errorf("spec: not a wasm module: %w", ErrMalformed)
	}
	i := 8
	for i < len(wasm) {
		id := wasm[i]
		i++
		size, next, err := uleb32(wasm, i)
		if err != nil {
			return nil, err
		}
		i = next
		if uint64(i)+uint64(size) > uint64(len(wasm)) {
			return nil, fmt.Errorf("spec: section runs past the end of the module: %w", ErrMalformed)
		}
		end := i + int(size)
		if id == 0 {
			nameLen, start, err := uleb32(wasm, i)
			if err != nil {
				return nil, err
			}
			if uint64(start)+uint64(nameLen) > uint64(end) {
				return nil, fmt.Errorf("spec: custom section name runs past its section: %w", ErrMalformed)
			}
			if string(wasm[start:start+int(nameLen)]) == SectionName {
				return wasm[start+int(nameLen) : end], nil
			}
		}
		i = end
	}
	return nil, ErrNoSpec
}

// uleb32 reads an unsigned LEB128 u32 at b[i:], returning the value and the
// index after it. At most 5 bytes encode a u32.
func uleb32(b []byte, i int) (uint32, int, error) {
	var v uint64
	for n := 0; n < 5; n++ {
		if i >= len(b) {
			return 0, 0, fmt.Errorf("spec: truncated LEB128: %w", ErrMalformed)
		}
		c := b[i]
		i++
		v |= uint64(c&0x7f) << (7 * n)
		if c&0x80 == 0 {
			if v > 0xffffffff {
				return 0, 0, fmt.Errorf("spec: LEB128 value overflows u32: %w", ErrMalformed)
			}
			return uint32(v), i, nil
		}
	}
	return 0, 0, fmt.Errorf("spec: LEB128 longer than 5 bytes: %w", ErrMalformed)
}

// Parse reads the function declarations from the contents of a contract
// spec section. Entries other than functions (types, errors, events) are
// skipped. A function name declared twice is dropped as ambiguous.
func Parse(section []byte) (Spec, error) {
	if len(section) > MaxWasmBytes {
		return Spec{}, fmt.Errorf("spec: section is %d bytes, over the %d-byte limit: %w", len(section), MaxWasmBytes, ErrMalformed)
	}
	r := bytes.NewReader(section)
	d := xdr3.NewDecoderWithOptions(r, xdr3.DecodeOptions{MaxDepth: maxSpecDepth, MaxInputLen: len(section)})
	functions := map[string]Function{}
	duplicate := map[string]bool{}
	for r.Len() > 0 {
		var e xdr.ScSpecEntry
		if _, err := e.DecodeFrom(d, maxSpecDepth); err != nil {
			return Spec{}, fmt.Errorf("spec: decode entry: %v: %w", err, ErrMalformed)
		}
		f, ok := e.GetFunctionV0()
		if !ok {
			continue
		}
		name := string(f.Name)
		if _, seen := functions[name]; seen || duplicate[name] {
			delete(functions, name)
			duplicate[name] = true
			continue
		}
		fn := Function{Name: name, Inputs: make([]Input, len(f.Inputs))}
		for i, in := range f.Inputs {
			fn.Inputs[i] = Input{Name: in.Name, Type: in.Type}
		}
		functions[name] = fn
	}
	return Spec{functions: functions}, nil
}

// Matches reports whether each argument has the type its input declares.
// Concrete types are checked exactly. Compound types are checked one level
// deep (a vec is a vector, a map is a map, an option is void or its inner
// type). Val and user-defined types are not checked, because their encodings
// are not fixed by the spec entry alone.
func (f Function) Matches(args []xdr.ScVal) bool {
	if len(args) != len(f.Inputs) {
		return false
	}
	for i := range args {
		if !typeMatches(f.Inputs[i].Type, args[i]) {
			return false
		}
	}
	return true
}

func typeMatches(t xdr.ScSpecTypeDef, v xdr.ScVal) bool {
	switch t.Type {
	case xdr.ScSpecTypeScSpecTypeVal, xdr.ScSpecTypeScSpecTypeUdt:
		return true
	case xdr.ScSpecTypeScSpecTypeBool:
		return v.Type == xdr.ScValTypeScvBool
	case xdr.ScSpecTypeScSpecTypeVoid:
		return v.Type == xdr.ScValTypeScvVoid
	case xdr.ScSpecTypeScSpecTypeError:
		return v.Type == xdr.ScValTypeScvError
	case xdr.ScSpecTypeScSpecTypeU32:
		return v.Type == xdr.ScValTypeScvU32
	case xdr.ScSpecTypeScSpecTypeI32:
		return v.Type == xdr.ScValTypeScvI32
	case xdr.ScSpecTypeScSpecTypeU64:
		return v.Type == xdr.ScValTypeScvU64
	case xdr.ScSpecTypeScSpecTypeI64:
		return v.Type == xdr.ScValTypeScvI64
	case xdr.ScSpecTypeScSpecTypeTimepoint:
		return v.Type == xdr.ScValTypeScvTimepoint
	case xdr.ScSpecTypeScSpecTypeDuration:
		return v.Type == xdr.ScValTypeScvDuration
	case xdr.ScSpecTypeScSpecTypeU128:
		return v.Type == xdr.ScValTypeScvU128
	case xdr.ScSpecTypeScSpecTypeI128:
		return v.Type == xdr.ScValTypeScvI128
	case xdr.ScSpecTypeScSpecTypeU256:
		return v.Type == xdr.ScValTypeScvU256
	case xdr.ScSpecTypeScSpecTypeI256:
		return v.Type == xdr.ScValTypeScvI256
	case xdr.ScSpecTypeScSpecTypeBytes:
		return v.Type == xdr.ScValTypeScvBytes
	case xdr.ScSpecTypeScSpecTypeBytesN:
		return v.Type == xdr.ScValTypeScvBytes && v.Bytes != nil && t.BytesN != nil && uint32(len(*v.Bytes)) == uint32(t.BytesN.N)
	case xdr.ScSpecTypeScSpecTypeString:
		return v.Type == xdr.ScValTypeScvString
	case xdr.ScSpecTypeScSpecTypeSymbol:
		return v.Type == xdr.ScValTypeScvSymbol
	case xdr.ScSpecTypeScSpecTypeAddress, xdr.ScSpecTypeScSpecTypeMuxedAddress:
		return v.Type == xdr.ScValTypeScvAddress
	case xdr.ScSpecTypeScSpecTypeOption:
		if v.Type == xdr.ScValTypeScvVoid {
			return true
		}
		return t.Option != nil && typeMatches(t.Option.ValueType, v)
	case xdr.ScSpecTypeScSpecTypeVec, xdr.ScSpecTypeScSpecTypeTuple:
		return v.Type == xdr.ScValTypeScvVec
	case xdr.ScSpecTypeScSpecTypeMap:
		return v.Type == xdr.ScValTypeScvMap
	case xdr.ScSpecTypeScSpecTypeResult:
		// A result is a return type; it has no argument encoding to check.
		return false
	}
	return false
}
