package explain_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// markedBlock matches a README block introduced by an HTML comment marker,
// `<!-- kind: name -->`, followed by a fenced block (snapshot, example) or a
// bare Markdown table (evidence).
var (
	fencedMarker = regexp.MustCompile("(?s)<!-- (snapshot|example): ([^ ]+) -->\n```[a-z]*\n(.*?)```\n")
	tableMarker  = regexp.MustCompile(`(?s)<!-- evidence: summary-table -->\n((?:\|[^\n]*\n)+)`)
	boldPercent  = regexp.MustCompile(`\*\*(\d+\.\d%)\*\*`)
)

// TestReadmeMatchesSources keeps the README's examples and numbers from
// drifting away from what the code and the evidence say. Each marked
// snapshot block must equal its committed snapshot, the Go example must
// equal ExampleExplain's body (which go test runs and checks), the hit-rate
// table must equal the one in docs/EVIDENCE.md, and every bold percentage in
// the prose must appear in that table.
func TestReadmeMatchesSources(t *testing.T) {
	readme := mustRead(t, "README.md")

	blocks := fencedMarker.FindAllStringSubmatch(readme, -1)
	kinds := map[string]int{}
	for _, b := range blocks {
		kind, name, content := b[1], b[2], b[3]
		kinds[kind]++
		var want string
		switch kind {
		case "snapshot":
			want = mustRead(t, "testdata/snapshots/"+name)
		case "example":
			want = exampleBody(t, name)
		}
		if content != want {
			t.Errorf("README %s block %s differs from its source\n--- README ---\n%s\n--- source ---\n%s", kind, name, content, want)
		}
	}
	if kinds["snapshot"] < 4 || kinds["example"] < 1 {
		t.Fatalf("README markers missing: %v", kinds)
	}

	m := tableMarker.FindStringSubmatch(readme)
	if m == nil {
		t.Fatal("README has no evidence summary table")
	}
	evidence := mustRead(t, "docs/EVIDENCE.md")
	summary := evidence[strings.Index(evidence, "## Summary"):strings.Index(evidence, "## How decoded")]
	var want []string
	for _, l := range strings.Split(summary, "\n") {
		if strings.HasPrefix(l, "|") {
			want = append(want, l)
		}
	}
	if m[1] != strings.Join(want, "\n")+"\n" {
		t.Errorf("README hit-rate table differs from docs/EVIDENCE.md\n--- README ---\n%s--- evidence ---\n%s", m[1], strings.Join(want, "\n"))
	}
	for _, p := range boldPercent.FindAllStringSubmatch(readme, -1) {
		if !strings.Contains(m[1], "("+p[1]+")") {
			t.Errorf("README states %s, which is not in the evidence table", p[1])
		}
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// exampleBody returns the body of an Example function in example_test.go,
// up to its Output comment, with one level of indentation removed.
func exampleBody(t *testing.T, name string) string {
	t.Helper()
	src := mustRead(t, "example_test.go")
	start := strings.Index(src, "func "+name+"() {\n")
	if start < 0 {
		t.Fatalf("no %s in example_test.go", name)
	}
	body := src[start+len("func "+name+"() {\n"):]
	body = body[:strings.Index(body, "\t// Output:")]
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimPrefix(l, "\t")
	}
	return strings.Join(lines, "\n") + "\n"
}
