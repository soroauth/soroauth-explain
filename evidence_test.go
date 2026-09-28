package explain_test

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	explain "github.com/soroauth/soroauth-explain"
	"github.com/soroauth/soroauth-explain/internal/snapshot"
	"github.com/soroauth/soroauth-explain/render"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// liveRecordPaths are the committed live runs, in the order the evidence
// document presents them.
var liveRecordPaths = []string{"testdata/live/testnet.json", "testdata/live/public.json"}

const evidencePath = "docs/EVIDENCE.md"

// LiveRendering is one explanation of a live entry, as recorded. Only the
// text rendering is kept; the JSON rendering carries the same information
// and is covered by the snapshot gate.
type LiveRendering struct {
	Confidence explain.Confidence `json:"confidence"`
	Error      string             `json:"error,omitempty"`
	Text       string             `json:"text,omitempty"`
}

// LiveCheck is the independent check of one decoded action.
type LiveCheck struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Result string `json:"result"`
	Detail string `json:"detail"`
}

// LiveRecord is one authorization entry from a real transaction.
type LiveRecord struct {
	TxHash     string         `json:"tx_hash"`
	Ledger     uint32         `json:"ledger"`
	TxStatus   string         `json:"tx_status"`
	Operation  int            `json:"operation"`
	AuthIndex  int            `json:"auth_index"`
	EntryXDR   string         `json:"entry_xdr"`
	EventAsset []string       `json:"event_assets,omitempty"`
	Default    LiveRendering  `json:"default"`
	WithAssets *LiveRendering `json:"with_event_assets,omitempty"`
	Checks     []LiveCheck    `json:"checks,omitempty"`
}

// LiveRun is one recorded run against one network.
type LiveRun struct {
	Name                string       `json:"name"`
	Network             string       `json:"network"`
	RPC                 string       `json:"rpc"`
	Fetched             string       `json:"fetched"`
	FirstLedger         uint32       `json:"first_ledger"`
	LastLedger          uint32       `json:"last_ledger"`
	Windows             []uint32     `json:"window_start_ledgers"`
	TransactionsScanned int          `json:"transactions_scanned"`
	Records             []LiveRecord `json:"records"`
}

// liveEntry is a recorded entry re-explained by the current code.
type liveEntry struct {
	rec        LiveRecord
	defaultExp *explain.Explanation
	assetsExp  *explain.Explanation // nil when the record has no event assets
}

// final is the explanation the independent checks ran against: with event
// assets when there were any, otherwise the default.
func (e liveEntry) final() *explain.Explanation {
	if e.assetsExp != nil {
		return e.assetsExp
	}
	return e.defaultExp
}

// reexplain explains a recorded entry again with the options the run used
// and requires the text to be exactly what was recorded. A change to the
// code that changes what the tool says about real entries therefore fails
// here until the live runs are recorded again.
func reexplain(t *testing.T, run LiveRun, rec LiveRecord) liveEntry {
	t.Helper()
	where := fmt.Sprintf("%s: %s op %d auth %d", run.Name, rec.TxHash, rec.Operation, rec.AuthIndex)
	var entry xdr.SorobanAuthorizationEntry
	if err := xdr.SafeUnmarshalBase64(rec.EntryXDR, &entry); err != nil {
		t.Fatalf("%s: %v", where, err)
	}
	one := func(recorded LiveRendering, opts ...explain.Option) *explain.Explanation {
		exp, err := explain.Explain(entry, opts...)
		if err != nil {
			if recorded.Error != err.Error() {
				t.Fatalf("%s: now refused with %q, recorded %q", where, err, recorded.Error)
			}
			return nil
		}
		if recorded.Error != "" || render.Text(exp) != recorded.Text || exp.Confidence != recorded.Confidence {
			t.Fatalf("%s: the current code no longer says what was recorded; record the live runs again\n--- now ---\n%s\n--- recorded ---\n%s",
				where, render.Text(exp), recorded.Text)
		}
		return &exp
	}
	le := liveEntry{rec: rec, defaultExp: one(rec.Default, explain.WithNetwork(run.Network))}
	if rec.WithAssets != nil {
		assets, err := snapshot.ParseAssets(rec.EventAsset)
		if err != nil {
			t.Fatalf("%s: %v", where, err)
		}
		le.assetsExp = one(*rec.WithAssets, explain.WithNetwork(run.Network), explain.WithAssets(assets...))
	}
	return le
}

// TestEvidenceUpToDate regenerates docs/EVIDENCE.md from the committed live
// records and requires the committed file to match, so the evidence document
// can only say what the recorded runs show. Set SOROAUTH_WRITE_EVIDENCE=1 to
// rewrite it after recording new runs.
func TestEvidenceUpToDate(t *testing.T) {
	var runs []LiveRun
	entries := map[string][]liveEntry{}
	for _, path := range liveRecordPaths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var run LiveRun
		if err := json.Unmarshal(raw, &run); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if run.Name == "" || len(run.Records) == 0 {
			t.Fatalf("%s: empty run", path)
		}
		for _, rec := range run.Records {
			entries[run.Name] = append(entries[run.Name], reexplain(t, run, rec))
		}
		runs = append(runs, run)
	}
	want := renderEvidence(runs, entries)
	if os.Getenv("SOROAUTH_WRITE_EVIDENCE") == "1" {
		if err := os.WriteFile(evidencePath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatalf("%v (generate with SOROAUTH_WRITE_EVIDENCE=1 go test -run TestEvidenceUpToDate .)", err)
	}
	if string(got) != want {
		t.Fatalf("%s is not what the live records produce; regenerate with SOROAUTH_WRITE_EVIDENCE=1 go test -run TestEvidenceUpToDate .", evidencePath)
	}
}

// percent formats n/total to one decimal place with integer arithmetic.
func percent(n, total int) string {
	if total == 0 {
		return "0.0%"
	}
	tenths := (n*1000 + total/2) / total
	return fmt.Sprintf("%d.%d%%", tenths/10, tenths%10)
}

func confidenceOf(exp *explain.Explanation) string {
	if exp == nil {
		return "refused"
	}
	return string(exp.Confidence)
}

type distribution struct {
	def, assets map[string]int
	total       int
}

func distributionOf(es []liveEntry) distribution {
	d := distribution{def: map[string]int{}, assets: map[string]int{}, total: len(es)}
	for _, e := range es {
		d.def[confidenceOf(e.defaultExp)]++
		d.assets[confidenceOf(e.final())]++
	}
	return d
}

func renderEvidence(runs []LiveRun, entries map[string][]liveEntry) string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	w("# Evidence: what soroauth-explain says about real entries\n\n")
	w("Generated by `TestEvidenceUpToDate` in `evidence_test.go` from the recorded live runs in `testdata/live/`.\n")
	w("Do not edit by hand. The test explains every recorded entry again and fails if the current code does\n")
	w("not say exactly what was recorded, so this document always describes the code it ships with.\n\n")
	w("Each run is one recording of `TestLiveTestnet` or `TestLivePublic` (`live_test.go`, build tag `live`)\n")
	w("against real transactions. The document shows how often the tool can explain real authorization entries,\n")
	w("and lists every rendering the tool marked `decoded`, because each of those is a claim made to a user.\n\n")

	w("## Summary\n\n")
	w("*Default* is `Explain` with `WithNetwork` only: what a caller gets with no other input. *With event\n")
	w("assets* adds, through `WithAssets`, the `CODE:ISSUER` strings found in the transaction's own Stellar Asset\n")
	w("Contract event topics. Those are candidates only: each is derived to a contract ID and compared, and one\n")
	w("that does not derive to the contract under inspection is ignored.\n\n")
	w("| Run | Entries | Decoded, default | Decoded, with event assets | Opaque | Decoded actions checked | Checks passed |\n")
	w("|---|---:|---:|---:|---:|---:|---:|\n")
	for _, run := range runs {
		d := distributionOf(entries[run.Name])
		checked, passed := 0, 0
		for _, r := range run.Records {
			for _, c := range r.Checks {
				checked++
				if c.Result == "event-match" || c.Result == "ledger-match" {
					passed++
				}
			}
		}
		w("| %s | %d | %d (%s) | %d (%s) | %d (%s) | %d | %d |\n", run.Name, d.total,
			d.def["decoded"], percent(d.def["decoded"], d.total),
			d.assets["decoded"], percent(d.assets["decoded"], d.total),
			d.def["opaque"], percent(d.def["opaque"], d.total), checked, passed)
	}
	w("\nMost opaque entries are calls to application contracts whose functions are not in the registry, which\n")
	w("holds the SEP-41 token interface only. That is the tool saying what it does not know, not a failure to\n")
	w("decode bytes.\n\n")

	w("## How decoded actions are checked\n\n")
	w("Every action marked `decoded`, in either pass and at any depth of the call tree, is checked against data\n")
	w("that does not pass through this library's explanation code:\n\n")
	w("- **Token actions**: in a successful transaction, the host must have emitted the matching SEP-41 event from\n")
	w("  the same contract, with the same parties and the same raw amount, and an asset topic equal to the label;\n")
	w("  for an approval, the event's live_until_ledger must also equal the rendered ledger.\n")
	w("- **Contract creation**: the contract ID is derived in the test from the rendered deployer and salt, and the\n")
	w("  instance fetched from the ledger must run the rendered wasm hash.\n\n")
	w("A result of `no-events` means the transaction failed, so the host emitted no events to compare against.\n")
	w("Those renderings are not confirmed by this check; they are listed like every other decoded action, and are\n")
	w("counted as checked but not passed in the summary.\n\n")
	w("Reproduce with new samples (the networks move, so the numbers will differ):\n\n")
	w("```sh\nSOROAUTH_LIVE_RECORD=1 go test -tags live -run 'TestLive(Testnet|Public)' -v -count=1 -timeout 30m .\n")
	w("SOROAUTH_WRITE_EVIDENCE=1 go test -run TestEvidenceUpToDate .\n```\n\n")

	for _, run := range runs {
		renderRun(w, run, entries[run.Name])
	}

	w("## Limits of this evidence\n\n")
	w("- Each run is one sample of eight windows across one RPC retention range. Traffic changes.\n")
	w("- The checks show that each decoded rendering matches what the host did or stored for that transaction.\n")
	w("  They do not show the tool is correct on entries outside these samples.\n")
	w("- The library is unaudited.\n")
	return b.String()
}

func renderRun(w func(string, ...any), run LiveRun, es []liveEntry) {
	w("## Run: %s\n\n", run.Name)
	w("| | |\n|---|---|\n")
	w("| Network | `%s` |\n", run.Network)
	w("| RPC | %s |\n", run.RPC)
	w("| Fetched | %s |\n", run.Fetched)
	w("| RPC retention range | ledgers %d to %d |\n", run.FirstLedger, run.LastLedger)
	starts := make([]string, len(run.Windows))
	for i, s := range run.Windows {
		starts[i] = fmt.Sprint(s)
	}
	w("| Sample windows (start ledgers) | %s |\n", strings.Join(starts, ", "))
	w("| Transactions scanned | %d |\n", run.TransactionsScanned)
	w("| Authorization entries explained | %d |\n\n", len(es))

	d := distributionOf(es)
	w("### Confidence distribution\n\n")
	w("| Confidence | Default | %% | With event assets | %% |\n|---|---:|---:|---:|---:|\n")
	for _, c := range []string{"decoded", "partial", "opaque", "refused"} {
		w("| %s | %d | %s | %d | %s |\n", c, d.def[c], percent(d.def[c], d.total), d.assets[c], percent(d.assets[c], d.total))
	}
	w("\n")

	w("### What the sample is made of\n\n")
	creds, roots := map[string]int{}, map[string]int{}
	for _, e := range es {
		if e.defaultExp == nil {
			continue
		}
		creds[e.defaultExp.CredentialType]++
		if len(e.defaultExp.Actions) > 0 {
			a := e.defaultExp.Actions[0]
			name := a.Function
			if name == "" {
				name = string(a.Kind)
			}
			roots[name]++
		}
	}
	w("| Credential type | Entries |\n|---|---:|\n")
	for _, k := range sortedKeys(creds) {
		w("| `%s` | %d |\n", k, creds[k])
	}
	w("\nRoot function, ten most frequent of %d distinct:\n\n", len(roots))
	w("| Root function | Entries |\n|---|---:|\n")
	type kv struct {
		k string
		v int
	}
	var rs []kv
	for k, v := range roots {
		rs = append(rs, kv{k, v})
	}
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].v != rs[j].v {
			return rs[i].v > rs[j].v
		}
		return rs[i].k < rs[j].k
	})
	for i := 0; i < len(rs) && i < 10; i++ {
		w("| `%s` | %d |\n", rs[i].k, rs[i].v)
	}
	w("\n")

	checks := map[string]int{}
	for _, r := range run.Records {
		for _, c := range r.Checks {
			checks[c.Kind+" / "+c.Result]++
		}
	}
	w("### Independent checks\n\n")
	w("| Action kind / result | Count |\n|---|---:|\n")
	for _, k := range sortedKeys(checks) {
		w("| %s | %d |\n", k, checks[k])
	}
	if len(checks) == 0 {
		w("| (no decoded actions) | 0 |\n")
	}
	w("\n")

	w("### Every entry rendered decoded\n\n")
	w("Entries whose whole explanation was `decoded`. Each summary line is the action `Summary`, which the text\n")
	w("and JSON renderings share.\n\n")
	n := 0
	for _, e := range es {
		exp := e.final()
		if exp == nil || exp.Confidence != explain.ConfidenceDecoded {
			continue
		}
		n++
		pass := "with event assets"
		if e.defaultExp != nil && e.defaultExp.Confidence == explain.ConfidenceDecoded {
			pass = "default and with event assets"
		}
		r := e.rec
		w("%d. tx `%s`, ledger %d, %s, operation %d, entry %d (%s), decoded in: %s\n", n, r.TxHash, r.Ledger, r.TxStatus, r.Operation, r.AuthIndex, exp.CredentialType, pass)
		var walk func([]explain.Action, string)
		walk = func(as []explain.Action, indent string) {
			for _, a := range as {
				w("%s   - %s\n", indent, a.Summary)
				walk(a.Sub, indent+"  ")
			}
		}
		walk(exp.Actions, "")
		for _, c := range r.Checks {
			w("   - check %s: **%s**\n", c.Path, c.Result)
		}
	}
	if n == 0 {
		w("None.\n")
	}

	w("\n### Every decoded action inside an entry that is not decoded\n\n")
	w("An entry can be opaque or partial as a whole while some of its actions are decoded, typically a token\n")
	w("transfer or approval beneath an application call this library does not interpret. Each of these is shown\n")
	w("to a user as decoded, so each is listed with its check. Path is the action's position in the call tree.\n\n")
	m := 0
	for _, e := range es {
		exp := e.final()
		if exp == nil || exp.Confidence == explain.ConfidenceDecoded {
			continue
		}
		byPath := map[string]explain.Action{}
		var index func([]explain.Action, string)
		index = func(as []explain.Action, prefix string) {
			for i, a := range as {
				p := fmt.Sprintf("%s%d", prefix, i)
				byPath[p] = a
				index(a.Sub, p+".")
			}
		}
		index(exp.Actions, "")
		r := e.rec
		for _, c := range r.Checks {
			m++
			w("%d. tx `%s`, ledger %d, %s, operation %d, entry %d (%s, entry is %s), path %s\n", m, r.TxHash, r.Ledger, r.TxStatus, r.Operation, r.AuthIndex, exp.CredentialType, exp.Confidence, c.Path)
			w("   - %s\n", byPath[c.Path].Summary)
			w("   - check: **%s**\n", c.Result)
		}
	}
	if m == 0 {
		w("None.\n")
	}
	w("\n")
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
