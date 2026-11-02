// Package render turns an explain.Explanation into output for people (Text)
// and for programs (JSON).
//
// Both renderers are deterministic: the same Explanation always produces the
// same bytes. Nothing reads the clock, the locale or the environment, and
// nothing iterates a map.
package render

import (
	"strconv"
	"strings"

	explain "github.com/soroauth/soroauth-explain"
	soroauth "github.com/soroauth/soroauth-go"
)

// Text renders an Explanation for a person to read.
//
// Confidence is shown on every node: the explanation, every action, every
// field and every delegate. No opaque node is omitted or collapsed, and the
// "Not determined" section lists every sentence in Unexplained.
func Text(exp explain.Explanation) string {
	var b strings.Builder
	writeExplanation(&b, exp, true)
	return b.String()
}

// EnvelopeText renders an EnvelopeExplanation for a person to read.
//
// The transaction's fields come first, then every operation in order: what
// it invokes, and each authorization entry it carries, rendered as Text
// renders an entry. An operation this library does not interpret is listed
// with its type and marked opaque, never left out. Every reason, from the
// envelope and from each entry, is listed once under "Not determined",
// saying which operation and entry it belongs to.
func EnvelopeText(env explain.EnvelopeExplanation) string {
	var b strings.Builder
	b.WriteString("Transaction envelope " + tag(env.Confidence) + "\n")
	writeFields(&b, env.Transaction, "  ")
	b.WriteString("  Shown: the transaction's fields, what each operation invokes, and what each\n")
	b.WriteString("  authorization entry authorizes. Not shown: Soroban resource limits and footprint.\n")

	for _, op := range env.Operations {
		b.WriteString("\nOperation " + strconv.Itoa(op.Index) + ": " + op.Type + " " + tag(op.Confidence) + "\n")
		if op.Source != "" {
			b.WriteString("  operation source: " + op.Source + "\n")
		}
		if op.Invocation != nil {
			b.WriteString("  Invokes:\n")
			writeAction(&b, *op.Invocation, 2)
		}
		for i, e := range op.Entries {
			b.WriteString("\n  Entry " + strconv.Itoa(i) + ":\n")
			var eb strings.Builder
			writeExplanation(&eb, e, false)
			for _, line := range strings.SplitAfter(eb.String(), "\n") {
				if strings.TrimSpace(line) == "" {
					b.WriteString(line)
					continue
				}
				b.WriteString("    " + line)
			}
		}
	}

	if len(env.Unexplained) > 0 {
		b.WriteString("\nNot determined:\n")
		for _, s := range env.Unexplained {
			b.WriteString("  - " + s + "\n")
		}
	}
	return b.String()
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func tag(c explain.Confidence) string {
	return "[" + string(c) + "]"
}

// writeExplanation renders one entry. notes is false inside an envelope,
// whose own list already carries the entry's reasons.
func writeExplanation(b *strings.Builder, exp explain.Explanation, notes bool) {
	if exp.CredentialType == soroauth.CredentialTypeSourceAccount {
		// Who authorizes differs; what is authorized is rendered below
		// exactly as for any other entry.
		b.WriteString("Authorization entry: source_account credentials, authenticated by the transaction's envelope signature " + tag(exp.Confidence) + "\n")
		b.WriteString("  The entry carries no address, nonce, expiration or signature of its own.\n")
	} else {
		b.WriteString("Authorization entry: " + exp.CredentialType + " credentials for " + exp.Subject + " " + tag(exp.Confidence) + "\n")
		b.WriteString("  nonce:              " + strconv.FormatInt(exp.Nonce, 10) + "\n")
		b.WriteString("  valid until ledger: " + strconv.FormatUint(uint64(exp.ValidUntilLedger), 10) + "\n")
		b.WriteString("  address-bound:      " + yesNo(exp.AddressBound) + "\n")
		b.WriteString("  signed:             " + yesNo(exp.Signed) + "\n")
	}

	b.WriteString("\nAuthorizes:\n")
	for i := range exp.Actions {
		writeAction(b, exp.Actions[i], 1)
	}

	if len(exp.Delegates) > 0 {
		b.WriteString("\nDelegates (each signs the same payload as the entry, CAP-71-01):\n")
		writeDelegates(b, exp.Delegates, 1)
	}

	if notes && len(exp.Unexplained) > 0 {
		b.WriteString("\nNot determined:\n")
		for _, s := range exp.Unexplained {
			b.WriteString("  - " + s + "\n")
		}
	}
}

func writeAction(b *strings.Builder, a explain.Action, depth int) {
	indent := strings.Repeat("  ", depth)
	b.WriteString(indent + tag(a.Confidence) + " " + a.Summary + "\n")
	writeFields(b, a.Fields, indent+"    ")
	for i := range a.Sub {
		writeAction(b, a.Sub[i], depth+1)
	}
}

func writeFields(b *strings.Builder, fields []explain.Field, indent string) {
	width := 0
	for _, f := range fields {
		if len(f.Name) > width {
			width = len(f.Name)
		}
	}
	for _, f := range fields {
		line := indent + f.Name + strings.Repeat(" ", width-len(f.Name)) + " = " + f.Value
		if f.Raw != "" {
			line += " (raw " + f.Raw + ")"
		}
		b.WriteString(line + " " + tag(f.Confidence) + "\n")
	}
}

func writeDelegates(b *strings.Builder, ds []explain.Explanation, depth int) {
	indent := strings.Repeat("  ", depth)
	for _, d := range ds {
		signed := "unsigned"
		if d.Signed {
			signed = "signed"
		}
		b.WriteString(indent + tag(d.Confidence) + " " + d.Subject + " (" + signed + ")\n")
		writeDelegates(b, d.Delegates, depth+1)
	}
}
