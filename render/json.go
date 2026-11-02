package render

import (
	"bytes"
	"encoding/json"
	"fmt"

	explain "github.com/soroauth/soroauth-explain"
)

// JSON renders an Explanation as the stable machine format for wallets.
//
// Field names are the json tags on explain.Explanation, explain.Action and
// explain.Field. Object keys are sorted, output is indented two spaces and
// ends in a newline, and integers keep full precision. This is a wire
// format: once released, renaming a field is a breaking change and needs a
// CHANGELOG entry that says so.
func JSON(exp explain.Explanation) ([]byte, error) {
	return sortedJSON(exp)
}

// EnvelopeJSON renders an EnvelopeExplanation in the same stable format as
// JSON: sorted keys, two-space indent, exact integers, a trailing newline.
// Each entry inside it has exactly the shape JSON gives a lone entry.
func EnvelopeJSON(env explain.EnvelopeExplanation) ([]byte, error) {
	return sortedJSON(env)
}

func sortedJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("render: json: %w", err)
	}
	// Round-trip through generic values: encoding/json writes struct fields
	// in declaration order but map keys sorted, so this sorts every object.
	// UseNumber keeps an int64 nonce exact instead of passing it through a
	// float64.
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var generic any
	if err := dec.Decode(&generic); err != nil {
		return nil, fmt.Errorf("render: json: %w", err)
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(generic); err != nil {
		return nil, fmt.Errorf("render: json: %w", err)
	}
	return out.Bytes(), nil
}
