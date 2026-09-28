package render

import (
	"bytes"
	"math"
	"strings"
	"testing"

	explain "github.com/soroauth/soroauth-explain"
)

const (
	contractC = "CABAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAFNSZ"
	accountG  = "GAAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQDZ7H"
)

// sample is an explanation with every kind of node: a decoded root, an
// opaque sub-action, a field with a Raw value, nested delegates and
// unexplained sentences.
func sample() explain.Explanation {
	actions := []explain.Action{{
		Kind:       explain.ActionCreateContract,
		Confidence: explain.ConfidenceOpaque,
		Summary:    "Deploy the Stellar Asset Contract for native (stellar_asset)",
		Fields: []explain.Field{
			{Name: "asset", Value: "native", Confidence: explain.ConfidenceDecoded},
			{Name: "executable", Value: "stellar_asset", Raw: "1", Confidence: explain.ConfidenceDecoded},
		},
		Sub: []explain.Action{{
			Kind:       explain.ActionInvokeContract,
			Contract:   contractC,
			Function:   "do_thing",
			Confidence: explain.ConfidenceOpaque,
			Summary:    "Call do_thing on " + contractC + " with 1 argument",
			Fields: []explain.Field{
				{Name: "contract", Value: contractC, Confidence: explain.ConfidenceDecoded},
				{Name: "function", Value: "do_thing", Confidence: explain.ConfidenceDecoded},
				{Name: "arguments", Value: "1", Confidence: explain.ConfidenceDecoded},
				{Name: "arg[0]", Value: `sym("a b")`, Confidence: explain.ConfidenceOpaque},
			},
		}},
	}}
	notes := []string{"first reason", "second reason"}
	return explain.Explanation{
		Subject:          contractC,
		CredentialType:   "address_with_delegates",
		AddressBound:     true,
		Confidence:       explain.ConfidenceOpaque,
		Nonce:            math.MinInt64,
		ValidUntilLedger: math.MaxUint32,
		Actions:          actions,
		Delegates: []explain.Explanation{{
			Subject: accountG, CredentialType: explain.CredentialTypeDelegate, AddressBound: true,
			Confidence: explain.ConfidenceOpaque, Signed: true, Actions: actions, Unexplained: notes,
			Delegates: []explain.Explanation{{
				Subject: contractC, CredentialType: explain.CredentialTypeDelegate, AddressBound: true,
				Confidence: explain.ConfidenceOpaque, Actions: actions, Unexplained: notes,
			}},
		}},
		Unexplained: notes,
	}
}

func TestTextExact(t *testing.T) {
	want := `Authorization entry: address_with_delegates credentials for ` + contractC + ` [opaque]
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

Not determined:
  - first reason
  - second reason
`
	if got := Text(sample()); got != want {
		t.Fatalf("Text mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestTextSourceAccount(t *testing.T) {
	exp := sample()
	exp.CredentialType = "source_account"
	exp.Subject, exp.AddressBound, exp.Nonce, exp.ValidUntilLedger, exp.Delegates = "", false, 0, 0, nil
	got := Text(exp)
	wantHead := "Authorization entry: source_account credentials, authenticated by the transaction's envelope signature [opaque]\n" +
		"  The entry carries no address, nonce, expiration or signature of its own.\n" +
		"\nAuthorizes:\n" +
		"  [opaque] Deploy the Stellar Asset Contract for native (stellar_asset)\n"
	if !strings.HasPrefix(got, wantHead) {
		t.Fatalf("got\n%s\nwant prefix\n%s", got, wantHead)
	}
	for _, s := range []string{"do_thing", "Not determined:", "first reason"} {
		if !strings.Contains(got, s) {
			t.Errorf("source-account rendering omits %q", s)
		}
	}
}

// TestTextNeverOmits walks the explanation and requires every action
// summary, every field and every delegate to appear with its confidence.
func TestTextNeverOmits(t *testing.T) {
	exp := sample()
	out := Text(exp)
	var walk func(a explain.Action)
	walk = func(a explain.Action) {
		if !strings.Contains(out, "["+string(a.Confidence)+"] "+a.Summary+"\n") {
			t.Errorf("action %q missing or without confidence", a.Summary)
		}
		for _, f := range a.Fields {
			if !strings.Contains(out, "= "+f.Value) || !strings.Contains(out, f.Value) {
				t.Errorf("field %s missing", f.Name)
			}
		}
		for _, s := range a.Sub {
			walk(s)
		}
	}
	for _, a := range exp.Actions {
		walk(a)
	}
	for _, s := range exp.Unexplained {
		if !strings.Contains(out, "  - "+s+"\n") {
			t.Errorf("unexplained %q missing", s)
		}
	}
	if !strings.Contains(out, "[opaque] "+accountG+" (signed)") {
		t.Error("delegate missing")
	}
}

func TestRenderDeterministic(t *testing.T) {
	firstText := Text(sample())
	firstJSON, err := JSON(sample())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		j, _ := JSON(sample())
		if Text(sample()) != firstText || !bytes.Equal(j, firstJSON) {
			t.Fatal("rendering differs between runs")
		}
	}
}
