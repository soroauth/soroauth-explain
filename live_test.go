//go:build live

// The live tests explain authorization entries from real transactions and
// record what the tool said. They reach the network, so they only build with
// -tags live:
//
//	SOROAUTH_LIVE_RECORD=1 go test -tags live -run 'TestLive(Testnet|Public)' -v -count=1 -timeout 30m .
//
// With SOROAUTH_LIVE_RECORD=1 each run is written to testdata/live/<name>.json;
// then regenerate docs/EVIDENCE.md with
// SOROAUTH_WRITE_EVIDENCE=1 go test -run TestEvidenceUpToDate .
//
// Every decoded action is checked against the host's own contract events,
// or for a contract creation against ledger state, neither of which passes
// through this library.

package explain_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	explain "github.com/soroauth/soroauth-explain"
	"github.com/soroauth/soroauth-explain/render"
	soroauth "github.com/soroauth/soroauth-go"
	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// liveNetwork is one network the live tests sample.
type liveNetwork struct {
	name       string
	rpc        string
	passphrase string
	// txsPerWindow bounds the transactions scanned per window. Public
	// traffic is mostly classic operations, so a window may need more
	// transactions to reach its entry quota.
	txsPerWindow int
}

var (
	liveTestnet = liveNetwork{"testnet", "https://soroban-testnet.stellar.org", network.TestNetworkPassphrase, 3000}
	livePublic  = liveNetwork{"public", "https://mainnet.sorobanrpc.com", network.PublicNetworkPassphrase, 20000}
)

const (

	// Sampling. A single stretch of ledgers is dominated by whichever bots
	// were busy then (a first probe on 2026-09-28 was 420 of 437 entries from
	// price oracles), so the run takes liveWindows samples spread evenly
	// across the RPC's retention range, each up to liveEntriesPerWindow
	// entries or liveTxsPerWindow transactions.
	liveWindows          = 8
	liveEntriesPerWindow = 60
)

type rpcTx struct {
	Status        string `json:"status"`
	TxHash        string `json:"txHash"`
	Ledger        uint32 `json:"ledger"`
	EnvelopeXdr   string `json:"envelopeXdr"`
	ResultMetaXdr string `json:"resultMetaXdr"`
	Events        struct {
		ContractEventsXdr [][]string `json:"contractEventsXdr"`
	} `json:"events"`
}

func rpcCall(ctx context.Context, rpc, method string, params any, out any) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpc, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return err
	}
	if env.Error != nil {
		return errors.New(env.Error.Message)
	}
	return json.Unmarshal(env.Result, out)
}

func TestLiveTestnet(t *testing.T) { runLive(t, liveTestnet) }

func TestLivePublic(t *testing.T) { runLive(t, livePublic) }

func runLive(t *testing.T, net liveNetwork) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	// getTransactions reports the retention range alongside any page.
	var probe struct {
		LatestLedger uint32 `json:"latestLedger"`
		OldestLedger uint32 `json:"oldestLedger"`
	}
	var latest struct {
		Sequence uint32 `json:"sequence"`
	}
	if err := rpcCall(ctx, net.rpc, "getLatestLedger", map[string]any{}, &latest); err != nil {
		t.Fatalf("getLatestLedger: %v", err)
	}
	if err := rpcCall(ctx, net.rpc, "getTransactions", map[string]any{"startLedger": latest.Sequence - 10, "pagination": map[string]any{"limit": 1}}, &probe); err != nil {
		t.Fatalf("getTransactions: %v", err)
	}
	run := LiveRun{
		Name:        net.name,
		Network:     net.passphrase,
		RPC:         net.rpc,
		Fetched:     time.Now().UTC().Format(time.RFC3339),
		FirstLedger: probe.OldestLedger,
		LastLedger:  probe.LatestLedger,
	}
	span := (probe.LatestLedger - probe.OldestLedger - 100) / liveWindows
	for w := 0; w < liveWindows; w++ {
		start := probe.OldestLedger + 50 + uint32(w)*span
		run.Windows = append(run.Windows, start)
		entries, txs := 0, 0
		params := map[string]any{"startLedger": start, "pagination": map[string]any{"limit": 200}}
		for entries < liveEntriesPerWindow && txs < net.txsPerWindow {
			var page struct {
				Transactions []rpcTx `json:"transactions"`
				Cursor       string  `json:"cursor"`
			}
			if err := rpcCall(ctx, net.rpc, "getTransactions", params, &page); err != nil {
				t.Fatalf("getTransactions from %d: %v", start, err)
			}
			if len(page.Transactions) == 0 {
				break
			}
			for _, tx := range page.Transactions {
				if entries >= liveEntriesPerWindow {
					break
				}
				txs++
				run.TransactionsScanned++
				recs := liveRecords(ctx, t, net, tx)
				entries += len(recs)
				run.Records = append(run.Records, recs...)
			}
			params = map[string]any{"pagination": map[string]any{"cursor": page.Cursor, "limit": 200}}
		}
	}
	if len(run.Records) == 0 {
		t.Fatal("no authorization entries found")
	}

	summarizeLive(t, run)

	if os.Getenv("SOROAUTH_LIVE_RECORD") == "1" {
		path := "testdata/live/" + net.name + ".json"
		out, err := json.MarshalIndent(run, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("recorded %d entries to %s", len(run.Records), path)
	}
}

func liveRecords(ctx context.Context, t *testing.T, net liveNetwork, tx rpcTx) []LiveRecord {
	var env xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(tx.EnvelopeXdr, &env); err != nil {
		t.Fatalf("%s: envelope: %v", tx.TxHash, err)
	}
	var out []LiveRecord
	for opIndex, op := range env.Operations() {
		invoke, ok := op.Body.GetInvokeHostFunctionOp()
		if !ok {
			continue
		}
		var events []xdr.ContractEvent
		if opIndex < len(tx.Events.ContractEventsXdr) {
			for _, e := range tx.Events.ContractEventsXdr[opIndex] {
				var ev xdr.ContractEvent
				if err := xdr.SafeUnmarshalBase64(e, &ev); err != nil {
					t.Fatalf("%s: event: %v", tx.TxHash, err)
				}
				events = append(events, ev)
			}
		}
		assetLabels, assets := eventAssets(events)
		created := createdInstances(t, tx)

		for authIndex, entry := range invoke.Auth {
			b64, err := xdr.MarshalBase64(entry)
			if err != nil {
				t.Fatal(err)
			}
			rec := LiveRecord{
				TxHash: tx.TxHash, Ledger: tx.Ledger, TxStatus: tx.Status,
				Operation: opIndex, AuthIndex: authIndex, EntryXDR: b64, EventAsset: assetLabels,
			}
			exp, r := liveExplain(t, rec, entry, explain.WithNetwork(net.passphrase))
			rec.Default = r
			finalExp := exp
			if len(assets) > 0 {
				exp2, r2 := liveExplain(t, rec, entry, explain.WithNetwork(net.passphrase), explain.WithAssets(assets...))
				rec.WithAssets = &r2
				finalExp = exp2
			}
			if finalExp != nil {
				rec.Checks = checkDecoded(ctx, net, finalExp.Actions, "", tx.Status, events, created)
			}
			out = append(out, rec)
		}
	}
	return out
}

// eventAssets collects the CODE:ISSUER strings SAC events carry as topics.
// They are candidates only: WithAssets still derives each one and compares.
func eventAssets(events []xdr.ContractEvent) ([]string, []xdr.Asset) {
	seen := map[string]bool{}
	var labels []string
	for _, ev := range events {
		body, ok := ev.Body.GetV0()
		if !ok {
			continue
		}
		for _, topic := range body.Topics {
			s, ok := topic.GetStr()
			if !ok {
				continue
			}
			code, issuer, found := strings.Cut(string(s), ":")
			if !found || seen[string(s)] {
				continue
			}
			if _, err := xdr.NewCreditAsset(code, issuer); err != nil {
				continue
			}
			seen[string(s)] = true
			labels = append(labels, string(s))
		}
	}
	sort.Strings(labels)
	assets := make([]xdr.Asset, 0, len(labels))
	for _, l := range labels {
		code, issuer, _ := strings.Cut(l, ":")
		assets = append(assets, xdr.MustNewCreditAsset(code, issuer))
	}
	return labels, assets
}

func liveExplain(t *testing.T, rec LiveRecord, entry xdr.SorobanAuthorizationEntry, opts ...explain.Option) (*explain.Explanation, LiveRendering) {
	exp, err := explain.Explain(entry, opts...)
	if err != nil {
		// A limit refusal is an allowed outcome; anything else is a bug.
		if !errors.Is(err, explain.ErrDepthLimit) && !errors.Is(err, explain.ErrNodeLimit) {
			t.Errorf("%s op %d auth %d: Explain: %v", rec.TxHash, rec.Operation, rec.AuthIndex, err)
		}
		return nil, LiveRendering{Error: err.Error()}
	}
	liveInvariants(t, rec, exp)
	return &exp, LiveRendering{Confidence: exp.Confidence, Text: render.Text(exp)}
}

func liveInvariants(t *testing.T, rec LiveRecord, exp explain.Explanation) {
	where := fmt.Sprintf("%s op %d auth %d", rec.TxHash, rec.Operation, rec.AuthIndex)
	if exp.Confidence != explain.ConfidenceDecoded && len(exp.Unexplained) == 0 {
		t.Errorf("%s: %s with empty Unexplained", where, exp.Confidence)
	}
	floor := explain.ConfidenceDecoded
	var walk func([]explain.Action)
	walk = func(as []explain.Action) {
		for _, a := range as {
			floor = explain.Floor(floor, a.Confidence)
			walk(a.Sub)
		}
	}
	walk(exp.Actions)
	for _, d := range exp.Delegates {
		floor = explain.Floor(floor, d.Confidence)
	}
	if floor != exp.Confidence {
		t.Errorf("%s: confidence %s, floor of nodes %s", where, exp.Confidence, floor)
	}
	if len(exp.Actions) == 0 {
		t.Errorf("%s: no actions", where)
	}
}

func field(a explain.Action, name string) explain.Field {
	for _, f := range a.Fields {
		if f.Name == name {
			return f
		}
	}
	return explain.Field{}
}

func fieldRaw(a explain.Action, name string) string {
	f := field(a, name)
	if f.Raw != "" {
		return f.Raw
	}
	return f.Value
}

// checkDecoded matches every decoded token action against the contract
// events the host emitted for the same operation. The events come from the
// network, not from this library, so a match is independent evidence that
// the rendering names the right contract, parties and raw amount.
func checkDecoded(ctx context.Context, net liveNetwork, actions []explain.Action, prefix, status string, events []xdr.ContractEvent, created map[string]string) []LiveCheck {
	var out []LiveCheck
	for i, a := range actions {
		path := fmt.Sprintf("%s%d", prefix, i)
		if a.Confidence == explain.ConfidenceDecoded {
			out = append(out, checkOne(ctx, net, a, path, status, events, created))
		}
		out = append(out, checkDecoded(ctx, net, a.Sub, path+".", status, events, created)...)
	}
	return out
}

func checkOne(ctx context.Context, net liveNetwork, a explain.Action, path, status string, events []xdr.ContractEvent, created map[string]string) LiveCheck {
	c := LiveCheck{Path: path, Kind: string(a.Kind)}
	if a.Kind == explain.ActionCreateContract {
		return checkCreate(net, a, c, status, created)
	}
	var topic string
	var parties []string
	switch a.Kind {
	case "token_transfer", "token_transfer_from":
		topic, parties = "transfer", []string{fieldRaw(a, "from"), fieldRaw(a, "to")}
	case "token_approve":
		topic, parties = "approve", []string{fieldRaw(a, "from"), fieldRaw(a, "spender")}
	case "token_burn", "token_burn_from":
		topic, parties = "burn", []string{fieldRaw(a, "from")}
	default:
		c.Result, c.Detail = "not-checked", "no event check exists for this kind"
		return c
	}
	if status != "SUCCESS" {
		c.Result, c.Detail = "no-events", "transaction status "+status+"; the host emitted no contract events to compare"
		return c
	}
	amount := fieldRaw(a, "amount")
	asset := field(a, "asset").Value
	for _, ev := range events {
		if ev.ContractId == nil {
			continue
		}
		cid := *ev.ContractId
		contract, err := soroauth.FormatAddress(xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &cid})
		if err != nil || contract != a.Contract {
			continue
		}
		body, ok := ev.Body.GetV0()
		if !ok || len(body.Topics) < 1+len(parties) {
			continue
		}
		if sym, ok := body.Topics[0].GetSym(); !ok || string(sym) != topic {
			continue
		}
		match := true
		for j, p := range parties {
			addr, ok := body.Topics[1+j].GetAddress()
			if !ok {
				match = false
				break
			}
			s, err := soroauth.FormatAddress(addr)
			if err != nil || s != p {
				match = false
				break
			}
		}
		if !match || eventAmount(body.Data) != amount {
			continue
		}
		// approve's event data also carries live_until_ledger, which the
		// rendering states; it must agree too.
		if topic == "approve" {
			if want := field(a, "live_until_ledger").Value; eventLiveUntil(body.Data) != want {
				c.Result = "no-matching-event"
				c.Detail = fmt.Sprintf("approve event on %s has live_until_ledger %q, rendering says %q", contract, eventLiveUntil(body.Data), want)
				return c
			}
		}
		last, _ := body.Topics[len(body.Topics)-1].GetStr()
		c.Result = "event-match"
		c.Detail = fmt.Sprintf("host event %q on %s with parties %v, amount %s, asset topic %q (rendered asset %q)", topic, contract, parties, amount, string(last), asset)
		if string(last) != asset {
			c.Result = "asset-mismatch"
		}
		return c
	}
	// An authorization entry says what may be called, not what will be: a
	// contract can leave an authorized sub-call unmade. If the contract
	// emitted no event of this kind at all, the call did not run and there is
	// nothing to compare with. An event of this kind that does not match is a
	// contradiction.
	if !emitted(events, a.Contract, topic) {
		c.Result, c.Detail = "not-executed", fmt.Sprintf("%s emitted no %q event in this transaction; the authorized call did not run", a.Contract, topic)
		return c
	}
	c.Result, c.Detail = "no-matching-event", fmt.Sprintf("no %q event on %s with parties %v and amount %s", topic, a.Contract, parties, amount)
	return c
}

// eventLiveUntil reads live_until_ledger from SEP-41 approve event data:
// the second element of [amount, live_until_ledger], or the map key.
func eventLiveUntil(v xdr.ScVal) string {
	var u *xdr.ScVal
	switch v.Type {
	case xdr.ScValTypeScvVec:
		if v.Vec != nil && *v.Vec != nil && len(**v.Vec) > 1 {
			u = &(**v.Vec)[1]
		}
	case xdr.ScValTypeScvMap:
		if v.Map != nil && *v.Map != nil {
			for i, e := range **v.Map {
				if s, ok := e.Key.GetSym(); ok && string(s) == "live_until_ledger" {
					u = &(**v.Map)[i].Val
				}
			}
		}
	}
	if u == nil || u.Type != xdr.ScValTypeScvU32 || u.U32 == nil {
		return ""
	}
	return fmt.Sprint(uint32(*u.U32))
}

// eventAmount reads the amount from SEP-41 event data: an i128, a vector
// whose first element is the amount (approve), or a map with an amount key.
func eventAmount(v xdr.ScVal) string {
	switch v.Type {
	case xdr.ScValTypeScvI128:
		return i128Decimal(*v.I128)
	case xdr.ScValTypeScvVec:
		if v.Vec != nil && *v.Vec != nil && len(**v.Vec) > 0 {
			return eventAmount((**v.Vec)[0])
		}
	case xdr.ScValTypeScvMap:
		if v.Map != nil && *v.Map != nil {
			for _, e := range **v.Map {
				if s, ok := e.Key.GetSym(); ok && string(s) == "amount" {
					return eventAmount(e.Val)
				}
			}
		}
	}
	return ""
}

// i128Decimal reassembles an i128 here rather than through the library's
// own rendering, so the comparison stays independent of the code under test.
func i128Decimal(p xdr.Int128Parts) string {
	return bigFromParts(int64(p.Hi), uint64(p.Lo))
}

func summarizeLive(t *testing.T, run LiveRun) {
	count := func(pick func(LiveRecord) string) map[string]int {
		m := map[string]int{}
		for _, r := range run.Records {
			m[pick(r)]++
		}
		return m
	}
	def := count(func(r LiveRecord) string {
		if r.Default.Error != "" {
			return "refused"
		}
		return string(r.Default.Confidence)
	})
	withAssets := count(func(r LiveRecord) string {
		if r.WithAssets == nil {
			if r.Default.Error != "" {
				return "refused"
			}
			return string(r.Default.Confidence)
		}
		if r.WithAssets.Error != "" {
			return "refused"
		}
		return string(r.WithAssets.Confidence)
	})
	checks := map[string]int{}
	for _, r := range run.Records {
		for _, c := range r.Checks {
			checks[c.Result]++
			if c.Result == "no-matching-event" || c.Result == "asset-mismatch" || c.Result == "ledger-mismatch" || c.Result == "check-error" {
				t.Errorf("%s op %d auth %d action %s: %s: %s", r.TxHash, r.Operation, r.AuthIndex, c.Path, c.Result, c.Detail)
			}
		}
	}
	t.Logf("retention %d-%d, windows from %v, %d transactions, %d entries", run.FirstLedger, run.LastLedger, run.Windows, run.TransactionsScanned, len(run.Records))
	t.Logf("default confidence: %v", def)
	t.Logf("with event assets:  %v", withAssets)
	t.Logf("decoded action checks: %v", checks)
}

func bigFromParts(hi int64, lo uint64) string {
	n := new(big.Int).Lsh(big.NewInt(hi), 64)
	return n.Add(n, new(big.Int).SetUint64(lo)).String()
}

// checkCreate verifies a decoded contract creation against the instance
// the transaction itself created, read from its result meta. It derives the
// new contract's ID from the rendered deployer and salt (the SHA-256 of an
// ENVELOPE_TYPE_CONTRACT_ID preimage, computed here rather than by the
// library) and requires the meta to show that contract created with the
// rendered executable. The meta records the transaction's own effect, so a
// later upgrade of the contract cannot change the answer, as comparing with
// current ledger state did (advisory run 36497973651).
func checkCreate(net liveNetwork, a explain.Action, c LiveCheck, status string, created map[string]string) LiveCheck {
	if status != "SUCCESS" {
		c.Result, c.Detail = "no-ledger-state", "transaction status "+status+"; no contract was created"
		return c
	}
	deployer, salt := field(a, "deployer").Value, field(a, "salt").Value
	exe := field(a, "executable").Value
	if deployer == "" || salt == "" || !strings.HasPrefix(exe, "wasm ") {
		c.Result, c.Detail = "not-checked", "only wasm contracts created from an address are checked; fields: "+exe
		return c
	}
	addr, err := soroauth.ParseAddress(deployer)
	if err != nil {
		c.Result, c.Detail = "check-error", err.Error()
		return c
	}
	saltBytes, err := hex.DecodeString(salt)
	if err != nil || len(saltBytes) != 32 {
		c.Result, c.Detail = "check-error", "bad salt "+salt
		return c
	}
	var u xdr.Uint256
	copy(u[:], saltBytes)
	pre := xdr.HashIdPreimage{
		Type: xdr.EnvelopeTypeEnvelopeTypeContractId,
		ContractId: &xdr.HashIdPreimageContractId{
			NetworkId: xdr.Hash(sha256.Sum256([]byte(net.passphrase))),
			ContractIdPreimage: xdr.ContractIdPreimage{
				Type:        xdr.ContractIdPreimageTypeContractIdPreimageFromAddress,
				FromAddress: &xdr.ContractIdPreimageFromAddress{Address: addr, Salt: u},
			},
		},
	}
	raw, err := pre.MarshalBinary()
	if err != nil {
		c.Result, c.Detail = "check-error", err.Error()
		return c
	}
	id := xdr.ContractId(sha256.Sum256(raw))
	contract, _ := soroauth.FormatAddress(xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &id})
	got, ok := created[contract]
	if !ok {
		c.Result, c.Detail = "ledger-mismatch", "the transaction's meta shows no instance created at derived contract "+contract
		return c
	}
	if got != exe {
		c.Result, c.Detail = "ledger-mismatch", fmt.Sprintf("the transaction created %s running %s, rendering says %s", contract, got, exe)
		return c
	}
	c.Result = "ledger-match"
	c.Detail = fmt.Sprintf("the transaction created contract %s, derived from deployer %s and salt %s, running %s", contract, deployer, salt, got)
	return c
}

// createdInstances returns, from a transaction's result meta, every
// contract instance it created and the wasm it runs.
func createdInstances(t *testing.T, tx rpcTx) map[string]string {
	out := map[string]string{}
	if tx.ResultMetaXdr == "" {
		return out
	}
	var meta xdr.TransactionMeta
	if err := xdr.SafeUnmarshalBase64(tx.ResultMetaXdr, &meta); err != nil {
		t.Fatalf("%s: meta: %v", tx.TxHash, err)
	}
	var changes []xdr.LedgerEntryChange
	if m, ok := meta.GetV4(); ok {
		for _, op := range m.Operations {
			changes = append(changes, op.Changes...)
		}
	}
	if m, ok := meta.GetV3(); ok {
		for _, op := range m.Operations {
			changes = append(changes, op.Changes...)
		}
	}
	for _, ch := range changes {
		if ch.Type != xdr.LedgerEntryChangeTypeLedgerEntryCreated || ch.Created == nil {
			continue
		}
		cd, ok := ch.Created.Data.GetContractData()
		if !ok {
			continue
		}
		inst, ok := cd.Val.GetInstance()
		if !ok || inst.Executable.WasmHash == nil {
			continue
		}
		contract, err := soroauth.FormatAddress(cd.Contract)
		if err != nil {
			continue
		}
		out[contract] = "wasm " + hex.EncodeToString(inst.Executable.WasmHash[:])
	}
	return out
}

// emitted reports whether contract emitted any event whose first topic is
// the given symbol.
func emitted(events []xdr.ContractEvent, contract, topic string) bool {
	for _, ev := range events {
		if ev.ContractId == nil {
			continue
		}
		cid := *ev.ContractId
		c, err := soroauth.FormatAddress(xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &cid})
		if err != nil || c != contract {
			continue
		}
		body, ok := ev.Body.GetV0()
		if !ok || len(body.Topics) == 0 {
			continue
		}
		if sym, ok := body.Topics[0].GetSym(); ok && string(sym) == topic {
			return true
		}
	}
	return false
}

// fetchTx gets one transaction by hash, with its contract events taken from
// the result meta, in the shape getTransactions returns.
func fetchTx(ctx context.Context, t *testing.T, net liveNetwork, hash string) rpcTx {
	t.Helper()
	var res struct {
		Status        string `json:"status"`
		Ledger        uint32 `json:"ledger"`
		EnvelopeXdr   string `json:"envelopeXdr"`
		ResultMetaXdr string `json:"resultMetaXdr"`
	}
	if err := rpcCall(ctx, net.rpc, "getTransaction", map[string]any{"hash": hash}, &res); err != nil {
		t.Fatalf("getTransaction %s: %v", hash, err)
	}
	tx := rpcTx{Status: res.Status, TxHash: hash, Ledger: res.Ledger, EnvelopeXdr: res.EnvelopeXdr, ResultMetaXdr: res.ResultMetaXdr}
	var meta xdr.TransactionMeta
	if err := xdr.SafeUnmarshalBase64(res.ResultMetaXdr, &meta); err != nil {
		t.Fatal(err)
	}
	m, ok := meta.GetV4()
	if !ok {
		t.Fatalf("%s: meta version %d, want 4", hash, meta.V)
	}
	for _, op := range m.Operations {
		var evs []string
		for _, ev := range op.Events {
			b64, err := xdr.MarshalBase64(ev)
			if err != nil {
				t.Fatal(err)
			}
			evs = append(evs, b64)
		}
		tx.Events.ContractEventsXdr = append(tx.Events.ContractEventsXdr, evs)
	}
	return tx
}

// TestLiveCheckRegressions pins the two cases the advisory run 36497973651
// reported, both of which were flaws in the checks rather than wrong
// renderings. They stay checkable while the RPCs retain the transactions.
func TestLiveCheckRegressions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cases := []struct {
		name string
		net  liveNetwork
		hash string
		want map[string]string // action path -> check result
	}{
		// The contract was created running 528a..., as rendered, and later
		// upgraded; the check must read the transaction's own meta.
		{"create_then_upgraded", liveTestnet, "c24266269a186d13cbd07264580c6ae150eb1e00b995a3b38470ef0531de7f10",
			map[string]string{"0": "ledger-match"}},
		// The entry authorizes a burn on the SAC of 1:GB4P3...; the contract
		// never made that call, so there is no burn event to compare with.
		{"authorized_burn_not_executed", livePublic, "cc265b25c1b4dda49d69dfa522776de4b7a207dee7d69f6534b713b6808388f8",
			map[string]string{"0.1": "not-executed"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := fetchTx(ctx, t, tc.net, tc.hash)
			got := map[string]string{}
			for _, rec := range liveRecords(ctx, t, tc.net, tx) {
				for _, c := range rec.Checks {
					if rec.AuthIndex == 0 {
						got[c.Path] = c.Result
					}
					t.Logf("auth %d action %s: %s: %s", rec.AuthIndex, c.Path, c.Result, c.Detail)
					if c.Result == "no-matching-event" || c.Result == "ledger-mismatch" || c.Result == "asset-mismatch" || c.Result == "check-error" {
						t.Errorf("auth %d action %s: %s", rec.AuthIndex, c.Path, c.Result)
					}
				}
			}
			for path, want := range tc.want {
				if got[path] != want {
					t.Errorf("action %s: check %q, want %q", path, got[path], want)
				}
			}
		})
	}
}
