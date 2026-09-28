// Package interfaces holds the function signatures this library knows the
// meaning of, and matches invocations against them.
//
// It says what a call's arguments are, never what a contract is. A match
// means an invocation has the name, arity and argument types of a known
// interface function; it does not mean the contract implements that
// interface honestly. Whether a match may be rendered as fact is decided by
// the caller, from the contract's derived identity (see explain.AssetLabel),
// never from the match alone.
//
// The package is data and matching only. It does not build explain.Action
// values: explain imports this package, so the dependency cannot run the
// other way.
package interfaces

import "github.com/stellar/go-stellar-sdk/xdr"

// ArgType is the shape an argument must have for a signature to match.
type ArgType int

const (
	// ArgAddress is an ScvAddress. The address arm is not constrained here;
	// how it renders is the renderer's concern.
	ArgAddress ArgType = iota + 1

	// ArgMuxedAddress is an ScvAddress that may also carry the muxed-account
	// arm (SEP-41's MuxedAddress). On the wire it is the same ScVal type as
	// ArgAddress; the distinction documents intent.
	ArgMuxedAddress

	// ArgAmount is an ScvI128 token amount, in the token's smallest unit.
	ArgAmount

	// ArgLedger is an ScvU32 ledger sequence number.
	ArgLedger
)

// Param is one named argument of a signature.
type Param struct {
	Name string
	Type ArgType
}

// Signature is one known function: the interface it comes from, its name
// and parameters, and the templates that describe a matching call.
//
// Templates use {param} placeholders for each Param name, plus {contract}
// and {asset}. AssetSummary is used when the contract's asset is derived;
// Summary when it is not, in which case amounts are raw integers.
type Signature struct {
	Interface    string
	Kind         string
	Function     string
	Params       []Param
	AssetSummary string
	Summary      string
}

// Registry is an ordered set of signatures.
type Registry struct {
	signatures []Signature
}

// New returns a registry of the given signatures.
func New(signatures ...Signature) Registry {
	return Registry{signatures: append([]Signature(nil), signatures...)}
}

// Default is the registry Explain uses: the SEP-41 token interface.
func Default() Registry {
	return New(SEP41...)
}

// Lookup returns the signature an invocation matches. A match requires the
// function name, the argument count and every argument's type to agree. A
// name alone is never a match: a contract can name a function anything,
// and matching on names is how an impostor would be believed.
func (r Registry) Lookup(function string, args []xdr.ScVal) (Signature, bool) {
	for _, s := range r.signatures {
		if s.Function != function || len(s.Params) != len(args) {
			continue
		}
		if argsMatch(s.Params, args) {
			return s, true
		}
	}
	return Signature{}, false
}

func argsMatch(params []Param, args []xdr.ScVal) bool {
	for i := range params {
		if !typeMatches(params[i].Type, args[i]) {
			return false
		}
	}
	return true
}

func typeMatches(t ArgType, v xdr.ScVal) bool {
	switch t {
	case ArgAddress, ArgMuxedAddress:
		return v.Type == xdr.ScValTypeScvAddress && v.Address != nil
	case ArgAmount:
		return v.Type == xdr.ScValTypeScvI128 && v.I128 != nil
	case ArgLedger:
		return v.Type == xdr.ScValTypeScvU32 && v.U32 != nil
	}
	return false
}
