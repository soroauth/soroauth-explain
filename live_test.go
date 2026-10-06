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
	"encoding/base64"
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
	"github.com/soroauth/soroauth-explain/spec"
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
	topic, parties, ok := sacEventShape(a)
	if !ok {
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
		if !match || !eventDataMatches(a, body.Data) {
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
	// contract can leave an authorized call unmade, as a batch may for some
	// of its participants. If the contract emitted no event of this kind for
	// this action's first party, the call did not run and there is nothing to
	// compare with. An event of this kind for the same party that does not
	// match is a contradiction.
	// set_admin's event names no argument party (its admin is the
	// authorizer), so it is identified by topic alone.
	party := ""
	if len(parties) > 0 {
		party = parties[0]
	}
	if !emitted(events, a.Contract, topic, party) {
		c.Result, c.Detail = "not-executed", fmt.Sprintf("%s emitted no %q event for %q in this transaction; the authorized call did not run", a.Contract, topic, party)
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

// emitted reports whether contract emitted an event whose first topic is the
// given symbol and whose second topic is the given party. An empty party
// matches on the topic alone.
func emitted(events []xdr.ContractEvent, contract, topic, party string) bool {
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
		if sym, ok := body.Topics[0].GetSym(); !ok || string(sym) != topic {
			continue
		}
		if party == "" {
			return true
		}
		if len(body.Topics) < 2 {
			continue
		}
		if addr, ok := body.Topics[1].GetAddress(); ok {
			if p, err := soroauth.FormatAddress(addr); err == nil && p == party {
				return true
			}
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
	if res.Status == "NOT_FOUND" {
		// RPCs keep a window of recent ledgers. A transaction that has aged
		// out cannot be checked here any more; say so instead of failing on
		// an empty meta. Until these cases are recorded under testdata, this
		// regression does not run anywhere.
		t.Skipf("%s is no longer within %s's retention; this regression case cannot run until it is recorded offline", hash, net.rpc)
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
		// Twelve authorized KALE burns in one batch, six executed. The six
		// match their events exactly; the other six have no burn event for
		// their party, although the contract burned for others
		// (advisory runs 36560088637, 36560298385, 36560504740).
		{"batch_burns_partly_executed", livePublic, "55ea6c0a7c2ed70f5694bad2709c5b2d73a45f621b0febe9ba0d83112944e90e", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := fetchTx(ctx, t, tc.net, tc.hash)
			got := map[string]string{}
			results := map[string]int{}
			for _, rec := range liveRecords(ctx, t, tc.net, tx) {
				for _, c := range rec.Checks {
					if rec.AuthIndex == 0 {
						got[c.Path] = c.Result
					}
					results[c.Result]++
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
			if tc.name == "batch_burns_partly_executed" && (results["event-match"] != 6 || results["not-executed"] != 6) {
				t.Errorf("results %v, want 6 event-match and 6 not-executed", results)
			}
		})
	}
}

// TestLiveSpecs fetches, with the library's own spec.RPC, the spec of every
// contract called in the committed live records, and records them in
// testdata/live/<name>.specs.json. The entries are not re-sampled: the
// evidence test re-explains the same recorded entries with these specs, so
// the change in the opaque share is measured on the same traffic.
func TestLiveSpecs(t *testing.T) {
	for _, net := range []liveNetwork{liveTestnet, livePublic} {
		t.Run(net.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
			defer cancel()
			raw, err := os.ReadFile("testdata/live/" + net.name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var run LiveRun
			if err := json.Unmarshal(raw, &run); err != nil {
				t.Fatal(err)
			}
			contracts := map[string]bool{}
			for _, r := range run.Records {
				var e xdr.SorobanAuthorizationEntry
				if err := xdr.SafeUnmarshalBase64(r.EntryXDR, &e); err != nil {
					t.Fatal(err)
				}
				for _, c := range spec.Contracts(e) {
					contracts[c] = true
				}
			}
			out := LiveSpecs{Name: net.name, RPC: net.rpc, Fetched: time.Now().UTC().Format(time.RFC3339),
				Contracts: map[string]LiveContract{}, Sections: map[string]string{}}
			rpc := spec.RPC{URL: net.rpc}
			reasons := map[string]int{}
			for c := range contracts {
				wasm, err := rpc.Wasm(ctx, c)
				if err != nil {
					out.Contracts[c] = LiveContract{Error: err.Error()}
					reasons[firstLine(err)]++
					continue
				}
				sum := sha256.Sum256(wasm)
				h := hex.EncodeToString(sum[:])
				section, err := spec.Section(wasm)
				if err != nil {
					out.Contracts[c] = LiveContract{WasmSHA256: h, Error: err.Error()}
					reasons[firstLine(err)]++
					continue
				}
				if _, err := spec.Parse(section); err != nil {
					out.Contracts[c] = LiveContract{WasmSHA256: h, Error: err.Error()}
					reasons[firstLine(err)]++
					continue
				}
				out.Contracts[c] = LiveContract{WasmSHA256: h}
				out.Sections[h] = base64.StdEncoding.EncodeToString(section)
			}
			t.Logf("%s: %d contracts, %d with a readable spec, %d distinct wasm; no spec: %v",
				net.name, len(contracts), len(contracts)-sum(reasons), len(out.Sections), reasons)
			if os.Getenv("SOROAUTH_LIVE_RECORD") == "1" {
				b, err := json.MarshalIndent(out, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				path := "testdata/live/" + net.name + ".specs.json"
				if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("recorded %s", path)
			}
		})
	}
}

// firstLine reduces an error to its reason, without the contract address,
// for counting.
func firstLine(err error) string {
	s := err.Error()
	for _, marker := range []string{"does not run wasm", "returned 0 entries", "no contract spec section", "malformed"} {
		if strings.Contains(s, marker) {
			return marker
		}
	}
	return "other: " + s
}

func sum(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// sacEventShape returns the event the host emits for a decoded action on a
// Stellar Asset Contract: its first topic and the parties in the topics
// that follow, in order.
//
// The shapes follow the host, not CAP-46-6's text, which they no longer
// match. CAP-67 ("Unified Asset Events", Final; stellar-protocol
// core/cap-0067.md at 9cd70372, line 207) removed the admin from the mint,
// clawback and set_authorized topics, and made a transfer from or to the
// asset's issuer emit mint or burn instead of transfer (line 226). The host
// implements exactly that: rs-soroban-env
// soroban-env-host/src/builtin_contracts/stellar_asset_contract/event.rs at
// f5a9fb79 (changed in 9a24835e, "Non muxed info related SAC changes for
// CAP-0067"). Do not change these back toward CAP-46-6's documented topics:
// the public network emits the host's shape (a mint observed in tx
// cc265b25... had topics mint | to | asset, with no admin).
func sacEventShape(a explain.Action) (string, []string, bool) {
	switch a.Kind {
	case "token_transfer", "token_transfer_from":
		// transfer and transfer_from both call transfer_maybe_with_issuer
		// (contract.rs:224, :248), which checks in this order
		// (event.rs:54-62): from == to is a transfer; from is the issuer is
		// a mint to `to`; to is the issuer is a burn from `from`; anything
		// else is a transfer.
		from, to := fieldRaw(a, "from"), fieldRaw(a, "to")
		issuer := labelIssuer(field(a, "asset").Value)
		switch {
		case from == to:
		case issuer != "" && from == issuer:
			return "mint", []string{to}, true // event.rs:122
		case issuer != "" && to == issuer:
			return "burn", []string{from}, true // event.rs:164
		}
		return "transfer", []string{from, to}, true // event.rs:101-107
	case "token_approve":
		return "approve", []string{fieldRaw(a, "from"), fieldRaw(a, "spender")}, true // event.rs:35-41
	case "token_burn", "token_burn_from":
		return "burn", []string{fieldRaw(a, "from")}, true // event.rs:164
	case "sac_mint":
		return "mint", []string{fieldRaw(a, "to")}, true // event.rs:122
	case "sac_clawback":
		return "clawback", []string{fieldRaw(a, "from")}, true // event.rs:131-136
	case "sac_set_authorized":
		return "set_authorized", []string{fieldRaw(a, "id")}, true // event.rs:142-147
	case "sac_set_admin":
		// The one admin event that still carries the admin (event.rs:153-158):
		// topics set_admin | admin | asset, data new_admin. The admin is the
		// entry's authorizer, not an argument, so it is not checked here.
		return "set_admin", nil, true
	}
	return "", nil, false
}

// labelIssuer returns the issuer in a CODE:ISSUER asset label, or "" for
// native and unlabelled contracts. The native asset has no issuer, and the
// host's is_issuer returns false for it (event.rs:22).
func labelIssuer(label string) string {
	if _, issuer, ok := strings.Cut(label, ":"); ok {
		return issuer
	}
	return ""
}

// eventDataMatches compares an event's data with the rendered action, by
// what each event carries (event.rs at f5a9fb79): set_authorized's data is
// the authorize bool (:148), set_admin's is the new admin's address (:159),
// and every other SAC event's is an amount, as an i128 or, for a muxed
// recipient, a map with an amount key (:67-92).
func eventDataMatches(a explain.Action, data xdr.ScVal) bool {
	switch a.Kind {
	case "sac_set_authorized":
		b, ok := data.GetB()
		return ok && fmt.Sprint(b) == field(a, "authorize").Value
	case "sac_set_admin":
		addr, ok := data.GetAddress()
		if !ok {
			return false
		}
		s, err := soroauth.FormatAddress(addr)
		return err == nil && s == fieldRaw(a, "new_admin")
	}
	return eventAmount(data) == fieldRaw(a, "amount")
}

// TestLiveSACEventShapes runs offline: it builds events in the host's shapes
// (event.rs at f5a9fb79) and checks the matcher against them. No real
// issuer transfer or admin call appears in the recorded samples, so these
// built events are the regression fixtures until one is recorded.
func TestLiveSACEventShapes(t *testing.T) {
	const (
		contract = "CAAL5BZAQ3W2G5CZX4HVLWNQIJISQ62ZNXXYCX2LONG4FFLRRYEYONZC"
		issuer   = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
		alice    = "GAEQ5IUNQTW36XMQF6MR2VWKPG3JOF6IKEGAD2JQ6OUNKTUVBAIE5AO3"
		bob      = "GAHKEAKBDDC467S3PFXPROVU6SBPQXDEWLUUTCPIIGD5MIO3KRRWS5HV"
		label    = "USDC:" + issuer
	)
	addr := func(s string) xdr.ScVal {
		a, err := soroauth.ParseAddress(s)
		if err != nil {
			t.Fatal(err)
		}
		return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &a}
	}
	sym := func(s string) xdr.ScVal {
		v := xdr.ScSymbol(s)
		return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &v}
	}
	str := func(s string) xdr.ScVal {
		v := xdr.ScString(s)
		return xdr.ScVal{Type: xdr.ScValTypeScvString, Str: &v}
	}
	i128 := func(n uint64) xdr.ScVal {
		return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &xdr.Int128Parts{Lo: xdr.Uint64(n)}}
	}
	boolean := func(b bool) xdr.ScVal { return xdr.ScVal{Type: xdr.ScValTypeScvBool, B: &b} }
	cid := func() *xdr.ContractId {
		a, err := soroauth.ParseAddress(contract)
		if err != nil {
			t.Fatal(err)
		}
		return a.ContractId
	}
	event := func(data xdr.ScVal, topics ...xdr.ScVal) xdr.ContractEvent {
		return xdr.ContractEvent{ContractId: cid(), Type: xdr.ContractEventTypeContract,
			Body: xdr.ContractEventBody{V: 0, V0: &xdr.ContractEventV0{Topics: topics, Data: data}}}
	}
	action := func(kind string, fields ...explain.Field) explain.Action {
		return explain.Action{Kind: explain.ActionKind(kind), Contract: contract, Confidence: explain.ConfidenceDecoded, Fields: fields}
	}
	f := func(name, value string) explain.Field { return explain.Field{Name: name, Value: value} }
	amount := explain.Field{Name: "amount", Value: "1.0000000", Raw: "10000000"}

	tests := []struct {
		name   string
		action explain.Action
		events []xdr.ContractEvent
		want   string
	}{
		{"transfer", action("token_transfer", f("asset", label), f("from", alice), f("to", bob), amount),
			[]xdr.ContractEvent{event(i128(10000000), sym("transfer"), addr(alice), addr(bob), str(label))}, "event-match"},
		// event.rs:56-57: a transfer from the issuer emits mint, not transfer.
		{"transfer_from_issuer_is_a_mint", action("token_transfer", f("asset", label), f("from", issuer), f("to", bob), amount),
			[]xdr.ContractEvent{event(i128(10000000), sym("mint"), addr(bob), str(label))}, "event-match"},
		// event.rs:58-59: a transfer to the issuer emits burn.
		{"transfer_to_issuer_is_a_burn", action("token_transfer_from", f("asset", label), f("spender", alice), f("from", bob), f("to", issuer), amount),
			[]xdr.ContractEvent{event(i128(10000000), sym("burn"), addr(bob), str(label))}, "event-match"},
		// event.rs:54-55: from == to is checked first, so it stays a transfer.
		{"issuer_to_itself_is_a_transfer", action("token_transfer", f("asset", label), f("from", issuer), f("to", issuer), amount),
			[]xdr.ContractEvent{event(i128(10000000), sym("transfer"), addr(issuer), addr(issuer), str(label))}, "event-match"},
		// The native asset has no issuer (event.rs:22).
		{"native_transfer", action("token_transfer", f("asset", "native"), f("from", alice), f("to", bob), amount),
			[]xdr.ContractEvent{event(i128(10000000), sym("transfer"), addr(alice), addr(bob), str("native"))}, "event-match"},
		{"mint", action("sac_mint", f("asset", label), f("to", bob), amount),
			[]xdr.ContractEvent{event(i128(10000000), sym("mint"), addr(bob), str(label))}, "event-match"},
		// CAP-46-6's documented shape, with the admin, is not what the host
		// emits; an event in that shape must not satisfy the check.
		{"mint_in_cap46_6_shape_does_not_match", action("sac_mint", f("asset", label), f("to", bob), amount),
			[]xdr.ContractEvent{event(i128(10000000), sym("mint"), addr(issuer), addr(bob), str(label))}, "not-executed"},
		{"clawback", action("sac_clawback", f("asset", label), f("from", alice), amount),
			[]xdr.ContractEvent{event(i128(10000000), sym("clawback"), addr(alice), str(label))}, "event-match"},
		{"set_authorized", action("sac_set_authorized", f("asset", label), f("id", alice), f("authorize", "false")),
			[]xdr.ContractEvent{event(boolean(false), sym("set_authorized"), addr(alice), str(label))}, "event-match"},
		{"set_authorized_wrong_value", action("sac_set_authorized", f("asset", label), f("id", alice), f("authorize", "true")),
			[]xdr.ContractEvent{event(boolean(false), sym("set_authorized"), addr(alice), str(label))}, "no-matching-event"},
		{"set_admin", action("sac_set_admin", f("asset", label), f("new_admin", bob)),
			[]xdr.ContractEvent{event(addr(bob), sym("set_admin"), addr(issuer), str(label))}, "event-match"},
		{"set_admin_wrong_new_admin", action("sac_set_admin", f("asset", label), f("new_admin", bob)),
			[]xdr.ContractEvent{event(addr(alice), sym("set_admin"), addr(issuer), str(label))}, "no-matching-event"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := checkOne(context.Background(), livePublic, tt.action, "0", "SUCCESS", tt.events, nil)
			if c.Result != tt.want {
				t.Fatalf("%s: %s, want %s", c.Result, c.Detail, tt.want)
			}
		})
	}
}
