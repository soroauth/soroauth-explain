package interfaces

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// sepFunctions parses every `fn name(env: Env, arg: Type, ...)` line of the
// verbatim SEP-41 trait in testdata into name -> params.
func sepFunctions(t testing.TB) map[string][][2]string {
	t.Helper()
	raw, err := os.ReadFile("testdata/sep-0041-interface.rs")
	if err != nil {
		t.Fatal(err)
	}
	fnLine := regexp.MustCompile(`^\s*fn (\w+)\(env: Env((?:, \w+: \w+)*)\)`)
	out := map[string][][2]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		m := fnLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		var params [][2]string
		for _, p := range strings.Split(strings.TrimPrefix(m[2], ", "), ", ") {
			if p == "" {
				continue
			}
			nt := strings.SplitN(p, ": ", 2)
			params = append(params, [2]string{nt[0], nt[1]})
		}
		out[m[1]] = params
	}
	return out
}

// sepType maps a SEP-41 Rust parameter type to the ArgType that encodes it.
var sepType = map[string]ArgType{
	"Address":      ArgAddress,
	"MuxedAddress": ArgMuxedAddress,
	"i128":         ArgAmount,
	"u32":          ArgLedger,
}

// TestSEP41MatchesTheSEP compares every registered signature, parameter by
// parameter, with the SEP's own text. A signature written from memory fails
// here.
func TestSEP41MatchesTheSEP(t *testing.T) {
	sep := sepFunctions(t)
	if len(sep) != 10 {
		t.Fatalf("parsed %d functions from the SEP text, want 10", len(sep))
	}
	for _, s := range SEP41 {
		t.Run(s.Function, func(t *testing.T) {
			want, ok := sep[s.Function]
			if !ok {
				t.Fatalf("%s is not in SEP-41", s.Function)
			}
			if len(want) != len(s.Params) {
				t.Fatalf("%d params, SEP-41 has %d", len(s.Params), len(want))
			}
			for i, p := range s.Params {
				if p.Name != want[i][0] || p.Type != sepType[want[i][1]] {
					t.Errorf("param %d = %s %v, SEP-41 has %s: %s", i, p.Name, p.Type, want[i][0], want[i][1])
				}
			}
			if s.Interface != "SEP-41" {
				t.Errorf("interface = %q", s.Interface)
			}
		})
	}
}

func TestSEP41Templates(t *testing.T) {
	placeholder := regexp.MustCompile(`\{(\w+)\}`)
	kinds := map[string]bool{}
	for _, s := range SEP41 {
		t.Run(s.Function, func(t *testing.T) {
			if kinds[s.Kind] {
				t.Errorf("duplicate kind %s", s.Kind)
			}
			kinds[s.Kind] = true
			names := map[string]bool{}
			for _, p := range s.Params {
				names[p.Name] = true
			}
			for tmplName, tmpl := range map[string]string{"AssetSummary": s.AssetSummary, "Summary": s.Summary} {
				used := map[string]bool{}
				for _, m := range placeholder.FindAllStringSubmatch(tmpl, -1) {
					used[m[1]] = true
					if !names[m[1]] && m[1] != "contract" && m[1] != "asset" {
						t.Errorf("%s uses {%s}, which is not a parameter", tmplName, m[1])
					}
				}
				// Every argument must be stated; a summary that drops one
				// hides part of what is authorized.
				for n := range names {
					if !used[n] {
						t.Errorf("%s omits {%s}", tmplName, n)
					}
				}
			}
			if !strings.Contains(s.AssetSummary, "{asset}") || strings.Contains(s.AssetSummary, "{contract}") {
				t.Errorf("AssetSummary must name the derived asset: %q", s.AssetSummary)
			}
			if !strings.Contains(s.Summary, "{contract}") || strings.Contains(s.Summary, "{asset}") {
				t.Errorf("Summary must name the contract, not an asset: %q", s.Summary)
			}
		})
	}
}
