package explain

import "errors"

// The sentinel errors this package returns. Every one is matched with
// errors.Is, and every function that can produce one wraps it with
// fmt.Errorf("explain: <operation>: %w", err), so the message says what was
// being attempted while the sentinel stays comparable.
//
// These are refusals. Where an entry cannot be explained without guessing,
// the package returns one of these rather than an explanation that sounds more
// certain than it is.
var (
	// ErrDepthLimit is returned when a walk over an entry's invocation tree
	// or its argument values nests deeper than the configured depth limit
	// (DefaultMaxDepth unless overridden with WithMaxDepth).
	//
	// The input is attacker-controlled. A limit hit refuses the whole entry
	// rather than truncating it, because a truncated explanation silently
	// omits whatever was hidden past the limit.
	ErrDepthLimit = errors.New("nesting exceeds the depth limit")

	// ErrNodeLimit is returned when a walk visits more nodes than the
	// configured node limit (DefaultMaxNodes unless overridden with
	// WithMaxNodes). It bounds the work, and the size of the rendering, for
	// an entry that is shallow but very wide.
	ErrNodeLimit = errors.New("entry exceeds the node limit")

	// ErrUnsupportedCredentials is returned for a credentials arm that
	// soroauth.Inspect does not recognise. The error also matches
	// soroauth.ErrUnsupportedCredentials, which it wraps.
	//
	// Explaining an entry built against a protocol this build does not
	// implement would mean guessing at a wire format that has not been read.
	ErrUnsupportedCredentials = errors.New("unsupported credentials type")

	// ErrEmptyArm is returned when a union's discriminant names an arm whose
	// value is missing, such as a contract-call invocation with a nil
	// ContractFn or an i128 ScVal with a nil I128.
	//
	// Decoded XDR cannot produce this; a value built in Go can. There is
	// nothing to explain in an arm that holds nothing, and rendering it as a
	// zero value would invent one.
	ErrEmptyArm = errors.New("union arm is empty")

	// ErrUnknownNetwork is returned when an asset derivation is asked for
	// without a network passphrase.
	//
	// A Stellar Asset Contract ID is derived from the asset and the network
	// passphrase together, so without the passphrase there is nothing to
	// derive and no contract may be labelled.
	ErrUnknownNetwork = errors.New("no network passphrase")

	// ErrNoSnapshot is returned by the snapshot gate when an input entry has
	// no committed snapshot for a rendering format.
	//
	// An entry without a snapshot is an entry whose rendering nobody has
	// reviewed. The gate refuses it rather than skipping it, so adding an
	// input without its expected output fails loudly.
	ErrNoSnapshot = errors.New("no committed snapshot")
)
