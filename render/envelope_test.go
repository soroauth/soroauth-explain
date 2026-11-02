package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	explain "github.com/soroauth/soroauth-explain"
)

// sampleEnvelope has a decoded field set, an interpreted invocation with an
// entry, and an operation this library does not interpret.
func sampleEnvelope() explain.EnvelopeExplanation {
	entry := sample()
	inv := entry.Actions[0].Sub[0]
	return explain.EnvelopeExplanation{
		Confidence: explain.ConfidenceOpaque,
		Transaction: []explain.Field{
			{Name: "envelope", Value: "transaction", Confidence: explain.ConfidenceDecoded},
			{Name: "source", Value: accountG, Confidence: explain.ConfidenceDecoded},
		},
		Operations: []explain.OperationExplanation{
			{Index: 0, Type: "InvokeHostFunction", Confidence: explain.ConfidenceOpaque, Invocation: &inv, Entries: []explain.Explanation{entry}},
			{Index: 1, Type: "Payment", Source: accountG, Confidence: explain.ConfidenceOpaque},
		},
		Unexplained: []string{"Operation 0: entry 0: first reason", "Operation 1: a Payment operation, which this library does not interpret."},
	}
}

// TestEnvelopeTextExact pins the layout: the transaction's fields and scope,
// each operation with its invocation and entries, and every reason once.
func TestEnvelopeTextExact(t *testing.T) {
	got := EnvelopeText(sampleEnvelope())
	want := `Transaction envelope [opaque]
  envelope = transaction [decoded]
  source   = ` + accountG + ` [decoded]
  Shown: the transaction's fields, what each operation invokes, and what each
  authorization entry authorizes. Not shown: Soroban resource limits and footprint.

Operation 0: InvokeHostFunction [opaque]
  Invokes:
    [opaque] Call do_thing on ` + contractC + ` with 1 argument
        contract  = ` + contractC + ` [decoded]
        function  = do_thing [decoded]
        arguments = 1 [decoded]
        arg[0]    = sym("a b") [opaque]

  Entry 0:
    Authorization entry: address_with_delegates credentials for ` + contractC + ` [opaque]
      nonce:              -9223372036854775808
      valid until ledger: 4294967295
      address-bound:      yes
      signed:             no

    Authorizes:
      [opaque] Deploy the Stellar Asset Contract for native (stellar_asset)
          asset      = native [decoded]
          executable = stellar_asset (raw 1) [decoded]
        [opaque] Call do_thing on ` + contractC + ` with 1 argument
            contract  = ` + contractC + ` [decoded]
            function  = do_thing [decoded]
            arguments = 1 [decoded]
            arg[0]    = sym("a b") [opaque]

    Delegates (each signs the same payload as the entry, CAP-71-01):
      [opaque] ` + accountG + ` (signed)
        [opaque] ` + contractC + ` (unsigned)

Operation 1: Payment [opaque]
  operation source: ` + accountG + `

Not determined:
  - Operation 0: entry 0: first reason
  - Operation 1: a Payment operation, which this library does not interpret.
`
	if got != want {
		t.Errorf("EnvelopeText mismatch\n--- got\n%s\n--- want\n%s", got, want)
	}
	if strings.Count(got, "first reason") != 1 {
		t.Error("an entry's reason is listed more than once")
	}
}

func TestEnvelopeJSON(t *testing.T) {
	env := sampleEnvelope()
	a, err := EnvelopeJSON(env)
	if err != nil {
		t.Fatal(err)
	}
	b, err := EnvelopeJSON(env)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) || !bytes.HasSuffix(a, []byte("}\n")) {
		t.Fatal("EnvelopeJSON is not deterministic or lacks its trailing newline")
	}
	var back explain.EnvelopeExplanation
	if err := json.Unmarshal(a, &back); err != nil {
		t.Fatal(err)
	}
	if back.Operations[0].Entries[0].Nonce != env.Operations[0].Entries[0].Nonce {
		t.Error("nonce lost precision")
	}
	// An entry inside an envelope has the shape JSON gives a lone entry.
	lone, err := JSON(env.Operations[0].Entries[0])
	if err != nil {
		t.Fatal(err)
	}
	var generic struct {
		Operations []struct {
			Entries []json.RawMessage `json:"entries"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(a, &generic); err != nil {
		t.Fatal(err)
	}
	var x, y any
	json.Unmarshal(lone, &x)
	json.Unmarshal(generic.Operations[0].Entries[0], &y)
	xs, _ := json.Marshal(x)
	ys, _ := json.Marshal(y)
	if !bytes.Equal(xs, ys) {
		t.Errorf("entry shape differs inside an envelope:\n%s\n%s", xs, ys)
	}
}
