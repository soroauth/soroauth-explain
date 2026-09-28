// Package explain turns a Soroban authorization entry into an account of what
// it authorizes, and says plainly what it could not determine.
//
// It is the sibling of github.com/soroauth/soroauth-go. That library proves
// that the right bytes get signed; this one describes what those bytes
// authorize. It never signs, holds a key, or reaches the network on the
// offline path.
//
// # Confidence
//
// Every explanation, action and field carries one of three confidence levels:
//
//	decoded  Every element came from the bytes or a checkable derivation.
//	         Example: "Transfer 100.0000000 XLM to GA7Q…VSGZ"
//	partial  The call shape is known, some arguments are not interpreted.
//	         Example: "Call swap on CA2G…CMEV with 4 arguments (2 not interpreted)"
//	opaque   The function is unknown to this library.
//	         Example: "Call do_thing on CA2G…CMEV with 3 arguments"
//
// Confidence does not average; it takes the floor. An explanation containing
// any partial node is at most partial, and a single opaque node anywhere keeps
// the whole explanation from being decoded.
//
// # The impostor guard
//
// A contract is only ever labelled with an asset code when its contract ID is
// the Stellar Asset Contract ID derived from that asset and the caller's
// network passphrase. Nothing a contract reports about itself, no name lookup,
// and no match on function signatures can earn it a label. Without a network
// passphrase, no contract is labelled at all.
package explain
