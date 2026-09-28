package interfaces

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

func addrVal(t xdr.ScAddressType) xdr.ScVal {
	a := xdr.ScAddress{Type: t}
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &a}
}

func i128Val() xdr.ScVal {
	return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &xdr.Int128Parts{Lo: 1}}
}

func u32Val() xdr.ScVal {
	u := xdr.Uint32(1)
	return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &u}
}

func TestLookup(t *testing.T) {
	account := addrVal(xdr.ScAddressTypeScAddressTypeAccount)
	muxed := addrVal(xdr.ScAddressTypeScAddressTypeMuxedAccount)
	sym := xdr.ScSymbol("x")
	symVal := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
	i64 := xdr.Int64(1)
	i64Val := xdr.ScVal{Type: xdr.ScValTypeScvI64, I64: &i64}

	tests := []struct {
		name     string
		function string
		args     []xdr.ScVal
		wantKind string
	}{
		{"transfer", "transfer", []xdr.ScVal{account, account, i128Val()}, "token_transfer"},
		{"transfer_to_muxed", "transfer", []xdr.ScVal{account, muxed, i128Val()}, "token_transfer"},
		{"approve", "approve", []xdr.ScVal{account, account, i128Val(), u32Val()}, "token_approve"},
		{"transfer_from", "transfer_from", []xdr.ScVal{account, account, account, i128Val()}, "token_transfer_from"},
		{"burn", "burn", []xdr.ScVal{account, i128Val()}, "token_burn"},
		{"burn_from", "burn_from", []xdr.ScVal{account, account, i128Val()}, "token_burn_from"},
		// A name alone is never a match.
		{"name_only_no_args", "transfer", nil, ""},
		{"name_only_wrong_arity", "transfer", []xdr.ScVal{account, account, i128Val(), i128Val()}, ""},
		{"name_only_wrong_type_amount_i64", "transfer", []xdr.ScVal{account, account, i64Val}, ""},
		{"name_only_wrong_type_symbol_address", "transfer", []xdr.ScVal{symVal, account, i128Val()}, ""},
		{"approve_ledger_as_i128", "approve", []xdr.ScVal{account, account, i128Val(), i128Val()}, ""},
		{"nil_arm", "burn", []xdr.ScVal{account, {Type: xdr.ScValTypeScvI128}}, ""},
		{"unregistered_read", "balance", []xdr.ScVal{account}, ""},
		{"case_differs", "Transfer", []xdr.ScVal{account, account, i128Val()}, ""},
	}
	r := Default()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, ok := r.Lookup(tt.function, tt.args)
			if ok != (tt.wantKind != "") || s.Kind != tt.wantKind {
				t.Fatalf("Lookup = (%q, %v), want %q", s.Kind, ok, tt.wantKind)
			}
		})
	}
}

func TestEmptyRegistryMatchesNothing(t *testing.T) {
	account := addrVal(xdr.ScAddressTypeScAddressTypeAccount)
	if _, ok := New().Lookup("transfer", []xdr.ScVal{account, account, i128Val()}); ok {
		t.Fatal("empty registry matched")
	}
}

func TestNewCopiesSignatures(t *testing.T) {
	sigs := []Signature{{Function: "f"}}
	r := New(sigs...)
	sigs[0].Function = "g"
	if _, ok := r.Lookup("f", nil); !ok {
		t.Fatal("registry aliased the caller's slice")
	}
}
