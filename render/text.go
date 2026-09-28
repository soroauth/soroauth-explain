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
	writeExplanation(&b, exp)
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

func writeExplanation(b *strings.Builder, exp explain.Explanation) {
	if exp.CredentialType == soroauth.CredentialTypeSourceAccount {
		b.WriteString("Authorization entry: source-account credentials " + tag(exp.Confidence) + "\n")
		b.WriteString("The transaction's source account authorizes this entry through the envelope signature.\n")
		b.WriteString("The entry carries no address, nonce, expiration or signature of its own.\n")
		return
	}

	b.WriteString("Authorization entry: " + exp.CredentialType + " credentials for " + exp.Subject + " " + tag(exp.Confidence) + "\n")
	b.WriteString("  nonce:              " + strconv.FormatInt(exp.Nonce, 10) + "\n")
	b.WriteString("  valid until ledger: " + strconv.FormatUint(uint64(exp.ValidUntilLedger), 10) + "\n")
	b.WriteString("  address-bound:      " + yesNo(exp.AddressBound) + "\n")
	b.WriteString("  signed:             " + yesNo(exp.Signed) + "\n")

	b.WriteString("\nAuthorizes:\n")
	for i := range exp.Actions {
		writeAction(b, exp.Actions[i], 1)
	}

	if len(exp.Delegates) > 0 {
		b.WriteString("\nDelegates (each signs the same payload as the entry, CAP-71-01):\n")
		writeDelegates(b, exp.Delegates, 1)
	}

	if len(exp.Unexplained) > 0 {
		b.WriteString("\nNot determined:\n")
		for _, s := range exp.Unexplained {
			b.WriteString("  - " + s + "\n")
		}
	}
}

func writeAction(b *strings.Builder, a explain.Action, depth int) {
	indent := strings.Repeat("  ", depth)
	b.WriteString(indent + tag(a.Confidence) + " " + a.Summary + "\n")
	width := 0
	for _, f := range a.Fields {
		if len(f.Name) > width {
			width = len(f.Name)
		}
	}
	for _, f := range a.Fields {
		line := indent + "    " + f.Name + strings.Repeat(" ", width-len(f.Name)) + " = " + f.Value
		if f.Raw != "" {
			line += " (raw " + f.Raw + ")"
		}
		b.WriteString(line + " " + tag(f.Confidence) + "\n")
	}
	for i := range a.Sub {
		writeAction(b, a.Sub[i], depth+1)
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
