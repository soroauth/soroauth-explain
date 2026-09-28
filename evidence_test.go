package explain_test

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	explain "github.com/soroauth/soroauth-explain"
)

const (
	liveRecordPath = "testdata/live/testnet.json"
	evidencePath   = "docs/EVIDENCE.md"
)

// LiveRendering is one explanation of a live entry.
type LiveRendering struct {
	Confidence explain.Confidence `json:"confidence"`
	Error      string             `json:"error,omitempty"`
	Text       string             `json:"text,omitempty"`
	JSON       json.RawMessage    `json:"json,omitempty"`
}

// LiveCheck is the independent check of one decoded token action.
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

// LiveRun is the whole recorded run.
type LiveRun struct {
	Network             string       `json:"network"`
	RPC                 string       `json:"rpc"`
	Fetched             string       `json:"fetched"`
	FirstLedger         uint32       `json:"first_ledger"`
	LastLedger          uint32       `json:"last_ledger"`
	Windows             []uint32     `json:"window_start_ledgers"`
	TransactionsScanned int          `json:"transactions_scanned"`
	Records             []LiveRecord `json:"records"`
}

// TestEvidenceUpToDate regenerates docs/EVIDENCE.md from the committed live
// record and requires the committed file to match, so the evidence document
// can only say what the recorded run shows. Set SOROAUTH_WRITE_EVIDENCE=1 to
// rewrite it after recording a new run.
func TestEvidenceUpToDate(t *testing.T) {
	raw, err := os.ReadFile(liveRecordPath)
	if err != nil {
		t.Fatal(err)
	}
	var run LiveRun
	if err := json.Unmarshal(raw, &run); err != nil {
		t.Fatal(err)
	}
	want := renderEvidence(run)
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
		t.Fatalf("%s is not what %s produces; regenerate with SOROAUTH_WRITE_EVIDENCE=1 go test -run TestEvidenceUpToDate .", evidencePath, liveRecordPath)
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

func confidenceOf(r LiveRendering) string {
	if r.Error != "" {
		return "refused"
	}
	return string(r.Confidence)
}

func withAssetsOf(r LiveRecord) LiveRendering {
	if r.WithAssets != nil {
		return *r.WithAssets
	}
	return r.Default
}

func renderEvidence(run LiveRun) string {
	var b strings.Builder
	total := len(run.Records)
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	w("# Evidence: what soroauth-explain says about real entries\n\n")
	w("Generated from `%s` by `TestEvidenceUpToDate` in `evidence_test.go`. Do not edit by hand.\n\n", liveRecordPath)
	w("This is one recorded run of `TestLiveTestnet` (`live_test.go`, build tag `live`) against real testnet\n")
	w("transactions. It shows how often the tool can explain real authorization entries, and it lists every\n")
	w("rendering the tool marked `decoded`, because each of those is a claim made to a user.\n\n")

	w("## The run\n\n")
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
	w("| Authorization entries explained | %d |\n\n", total)
	w("Reproduce with a new sample (the network moves, so the numbers will differ):\n\n")
	w("```sh\nSOROAUTH_LIVE_RECORD=%s go test -tags live -run TestLiveTestnet -v -count=1 -timeout 20m .\nSOROAUTH_WRITE_EVIDENCE=1 go test -run TestEvidenceUpToDate .\n```\n\n", liveRecordPath)

	w("## Confidence distribution\n\n")
	w("*Default* is `Explain` with `WithNetwork` only: what a caller gets with no other input. *With event\n")
	w("assets* adds, through `WithAssets`, the `CODE:ISSUER` strings found in the transaction's own Stellar Asset\n")
	w("Contract event topics. Those are candidates only: each is derived to a contract ID and compared, and one that\n")
	w("does not derive to the contract under inspection is ignored.\n\n")
	w("| Confidence | Default | %% | With event assets | %% |\n|---|---:|---:|---:|---:|\n")
	def, wa := map[string]int{}, map[string]int{}
	for _, r := range run.Records {
		def[confidenceOf(r.Default)]++
		wa[confidenceOf(withAssetsOf(r))]++
	}
	for _, c := range []string{"decoded", "partial", "opaque", "refused"} {
		w("| %s | %d | %s | %d | %s |\n", c, def[c], percent(def[c], total), wa[c], percent(wa[c], total))
	}
	w("\n")

	w("## What the sample is made of\n\n")
	creds := map[string]int{}
	roots := map[string]int{}
	for _, r := range run.Records {
		var exp explain.Explanation
		if len(r.Default.JSON) == 0 || json.Unmarshal(r.Default.JSON, &exp) != nil {
			continue
		}
		creds[exp.CredentialType]++
		if len(exp.Actions) > 0 {
			a := exp.Actions[0]
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
	w("\nMost opaque entries are calls to application contracts (price oracles dominate testnet in this sample)\n")
	w("whose functions are not in the registry, which holds the SEP-41 token interface only. That is the expected\n")
	w("output for an unknown function, not a failure to decode bytes.\n\n")

	w("## Independent checks of every decoded action\n\n")
	w("Every action marked `decoded`, in either pass and at any depth of the call tree, was checked against data\n")
	w("that does not pass through this library:\n\n")
	w("- **Token actions**: in a successful transaction, the host must have emitted the matching SEP-41 event from\n")
	w("  the same contract, with the same parties and the same raw amount, and an asset topic equal to the label;\n")
	w("  for an approval, the event's live_until_ledger must also equal the rendered ledger.\n")
	w("- **Contract creation**: the contract ID is derived in the test from the rendered deployer and salt, and the\n")
	w("  instance fetched from the ledger must run the rendered wasm hash.\n\n")
	checks := map[string]int{}
	for _, r := range run.Records {
		for _, c := range r.Checks {
			checks[c.Kind+" / "+c.Result]++
		}
	}
	w("| Action kind / result | Count |\n|---|---:|\n")
	for _, k := range sortedKeys(checks) {
		w("| %s | %d |\n", k, checks[k])
	}
	w("\n")

	w("## Every entry rendered decoded\n\n")
	w("Entries whose whole explanation was `decoded` in the pass shown. Each summary line is the recorded\n")
	w("action `Summary`, which the text and JSON renderings share.\n\n")
	n := 0
	for _, r := range run.Records {
		rend := withAssetsOf(r)
		if rend.Confidence != explain.ConfidenceDecoded || rend.Error != "" {
			continue
		}
		n++
		pass := "with event assets"
		if r.Default.Confidence == explain.ConfidenceDecoded {
			pass = "default and with event assets"
		}
		var exp explain.Explanation
		if err := json.Unmarshal(rend.JSON, &exp); err != nil {
			continue
		}
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

	w("\n## Every decoded action inside an entry that is not decoded\n\n")
	w("An entry can be opaque or partial as a whole while some of its actions are decoded, typically a token\n")
	w("transfer or approval beneath an application call this library does not interpret. Each of these is shown\n")
	w("to a user as decoded, so each is listed with its check. Path is the action's position in the call tree.\n\n")
	m := 0
	for _, r := range run.Records {
		rend := withAssetsOf(r)
		if rend.Error != "" || rend.Confidence == explain.ConfidenceDecoded {
			continue
		}
		var exp explain.Explanation
		if err := json.Unmarshal(rend.JSON, &exp); err != nil {
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
	w("\n## Limits of this evidence\n\n")
	w("- It is testnet, sampled over eight windows of one retention range; public-network traffic may differ.\n")
	w("- The checks show that each decoded rendering matches what the host did or stored for that transaction.\n")
	w("  They do not show the tool is correct on entries outside this sample.\n")
	w("- The library is unaudited.\n")
	return b.String()
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
