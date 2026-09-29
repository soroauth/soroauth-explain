package explain

import (
	"sort"
	"strings"
	"testing"
	"unicode"
)

// summaryVocabulary is every word a Summary template may contribute. Anything
// left in a Summary after removing every Field value must be one of these, so
// a Summary cannot state a value that no Field carries.
var summaryVocabulary = map[string]bool{
	"Call": true, "on": true, "with": true, "argument": true, "arguments": true,
	"the": true, "Stellar": true, "Asset": true, "Contract": true, "for": true,
	"Create": true, "a": true, "contract": true, "running": true, "deployed": true,
	"by": true, "salt": true, "Deploy": true, "from": true, "asset": true,
	"constructor": true,
	// SEP-41 templates (interfaces/sep41.go).
	"Allow": true, "spend": true, "up": true, "to": true, "units": true, "of": true,
	"token": true, "at": true, "until": true, "ledger": true, "replacing": true,
	"any": true, "current": true, "allowance": true, "Transfer": true,
	"spending": true, "Burn": true,
	// Spec-named calls (explainNamed).
	"named": true, "spec": true, "publishes": true,
}

// checkSummaryAgrees asserts that every value in a.Summary appears in some
// Field of a, recursively over a.Sub.
func checkSummaryAgrees(t testing.TB, a Action) {
	t.Helper()
	values := make([]string, 0, len(a.Fields))
	for _, f := range a.Fields {
		if f.Value != "" {
			values = append(values, f.Value)
		}
	}
	// Longest first, so a value that contains another is removed whole.
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	rest := a.Summary
	for _, v := range values {
		rest = strings.ReplaceAll(rest, v, " ")
	}
	words := strings.FieldsFunc(rest, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for _, w := range words {
		if !summaryVocabulary[w] {
			t.Errorf("summary %q states %q, which no field carries", a.Summary, w)
		}
	}
	for i := range a.Sub {
		checkSummaryAgrees(t, a.Sub[i])
	}
}

func TestSummarize(t *testing.T) {
	fields := []Field{{Name: "function", Value: "swap"}, {Name: "contract", Value: "CXYZ"}, {Name: "arguments", Value: "4"}}
	got := summarize("Call {function} on {contract} with {arguments} arguments", fields)
	if want := "Call swap on CXYZ with 4 arguments"; got != want {
		t.Fatalf("summarize = %q, want %q", got, want)
	}
}

// TestCheckSummaryAgreesBites proves the agreement check fails on a summary
// that states a value no field carries.
func TestCheckSummaryAgreesBites(t *testing.T) {
	bad := Action{
		Summary: "Call transfer on CXYZ with 3 arguments",
		Fields:  []Field{{Name: "contract", Value: "CXYZ"}, {Name: "arguments", Value: "3"}},
	}
	rec := &recordingTB{TB: t}
	checkSummaryAgrees(rec, bad)
	if !rec.failed {
		t.Fatal("check passed a summary naming a function no field carries")
	}
}

// recordingTB records Errorf instead of failing the enclosing test, so a
// test can assert that a check fails.
type recordingTB struct {
	testing.TB
	failed bool
}

func (r *recordingTB) Helper()                           {}
func (r *recordingTB) Errorf(format string, args ...any) { r.failed = true }
