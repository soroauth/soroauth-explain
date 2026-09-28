package explain

// Confidence says how much of a rendering came from the bytes.
//
// It is the mechanism behind the honesty boundary: a wallet shows a user a
// decoded rendering as a fact, and anything less as a claim with caveats. A
// string type keeps the JSON wire format readable and stable.
type Confidence string

const (
	// ConfidenceDecoded means every element came from the bytes or from a
	// derivation this library can check offline.
	ConfidenceDecoded Confidence = "decoded"

	// ConfidencePartial means the call shape is known but some arguments are
	// not interpreted.
	ConfidencePartial Confidence = "partial"

	// ConfidenceOpaque means the function is unknown to this library.
	ConfidenceOpaque Confidence = "opaque"
)

// rank orders confidences from least to most certain. Anything that is not
// one of the three defined values ranks below opaque, so an unknown value can
// never be mistaken for a better one.
func (c Confidence) rank() int {
	switch c {
	case ConfidenceDecoded:
		return 3
	case ConfidencePartial:
		return 2
	case ConfidenceOpaque:
		return 1
	}
	return 0
}

// Floor returns the lower of two confidences: opaque < partial < decoded.
//
// Confidence does not average. An explanation is only as certain as its least
// certain node, so every aggregation in this package goes through Floor. An
// unrecognised value on either side floors to ConfidenceOpaque rather than
// being passed through, because a value this package did not produce is not
// evidence of anything.
func Floor(a, b Confidence) Confidence {
	if a.rank() == 0 || b.rank() == 0 {
		return ConfidenceOpaque
	}
	if a.rank() < b.rank() {
		return a
	}
	return b
}
