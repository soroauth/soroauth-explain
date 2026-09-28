//go:build live

// The live test explains authorization entries from real testnet
// transactions and records what it said. It reaches the network, so it only
// builds with -tags live:
//
//	SOROAUTH_LIVE_RECORD=testdata/live/testnet.json go test -tags live -run TestLiveTestnet -v -count=1 -timeout 20m .
//
// Every decoded token action is checked against the host's own contract
// events for the same operation, which do not pass through this library.

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

const (
	liveRPC = "https://soroban-testnet.stellar.org"

	// Sampling. A single stretch of ledgers is dominated by whichever bots
	// were busy then (a first probe on 2026-09-28 was 420 of 437 entries from
	// price oracles), so the run takes liveWindows samples spread evenly
	// across the RPC's retention range, each up to liveEntriesPerWindow
	// entries or liveTxsPerWindow transactions.
	liveWindows          = 8
	liveEntriesPerWindow = 60
	liveTxsPerWindow     = 3000
)

type rpcTx struct {
	Status      string `json:"status"`
	TxHash      string `json:"txHash"`
	Ledger      uint32 `json:"ledger"`
	EnvelopeXdr string `json:"envelopeXdr"`
	Events      struct {
		ContractEventsXdr [][]string `json:"contractEventsXdr"`
	} `json:"events"`
}

func rpcCall(ctx context.Context, method string, params any, out any) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, liveRPC, bytes.NewReader(body))
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

func TestLiveTestnet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// getTransactions reports the retention range alongside any page.
	var probe struct {
		LatestLedger uint32 `json:"latestLedger"`
		OldestLedger uint32 `json:"oldestLedger"`
	}
	var latest struct {
		Sequence uint32 `json:"sequence"`
	}
	if err := rpcCall(ctx, "getLatestLedger", map[string]any{}, &latest); err != nil {
		t.Fatalf("getLatestLedger: %v", err)
	}
	if err := rpcCall(ctx, "getTransactions", map[string]any{"startLedger": latest.Sequence - 10, "pagination": map[string]any{"limit": 1}}, &probe); err != nil {
		t.Fatalf("getTransactions: %v", err)
	}
	run := LiveRun{
		Network:     network.TestNetworkPassphrase,
		RPC:         liveRPC,
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
		for entries < liveEntriesPerWindow && txs < liveTxsPerWindow {
			var page struct {
				Transactions []rpcTx `json:"transactions"`
				Cursor       string  `json:"cursor"`
			}
			if err := rpcCall(ctx, "getTransactions", params, &page); err != nil {
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
				recs := liveRecords(ctx, t, tx)
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

	if path := os.Getenv("SOROAUTH_LIVE_RECORD"); path != "" {
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

func liveRecords(ctx context.Context, t *testing.T, tx rpcTx) []LiveRecord {
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

		for authIndex, entry := range invoke.Auth {
			b64, err := xdr.MarshalBase64(entry)
			if err != nil {
				t.Fatal(err)
			}
			rec := LiveRecord{
				TxHash: tx.TxHash, Ledger: tx.Ledger, TxStatus: tx.Status,
				Operation: opIndex, AuthIndex: authIndex, EntryXDR: b64, EventAsset: assetLabels,
			}
			exp, r := liveExplain(t, rec, entry, explain.WithNetwork(network.TestNetworkPassphrase))
			rec.Default = r
			finalExp := exp
			if len(assets) > 0 {
				exp2, r2 := liveExplain(t, rec, entry, explain.WithNetwork(network.TestNetworkPassphrase), explain.WithAssets(assets...))
				rec.WithAssets = &r2
				finalExp = exp2
			}
			if finalExp != nil {
				rec.Checks = checkDecoded(ctx, finalExp.Actions, "", tx.Status, events)
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
	js, err := render.JSON(exp)
	if err != nil {
		t.Fatal(err)
	}
	return &exp, LiveRendering{Confidence: exp.Confidence, Text: render.Text(exp), JSON: js}
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
func checkDecoded(ctx context.Context, actions []explain.Action, prefix, status string, events []xdr.ContractEvent) []LiveCheck {
	var out []LiveCheck
	for i, a := range actions {
		path := fmt.Sprintf("%s%d", prefix, i)
		if a.Confidence == explain.ConfidenceDecoded {
			out = append(out, checkOne(ctx, a, path, status, events))
		}
		out = append(out, checkDecoded(ctx, a.Sub, path+".", status, events)...)
	}
	return out
}

func checkOne(ctx context.Context, a explain.Action, path, status string, events []xdr.ContractEvent) LiveCheck {
	c := LiveCheck{Path: path, Kind: string(a.Kind)}
	if a.Kind == explain.ActionCreateContract {
		return checkCreate(ctx, a, c, status)
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

// checkCreate verifies a decoded contract creation against ledger state. It
// derives the new contract's ID from the rendered deployer and salt (the
// SHA-256 of an ENVELOPE_TYPE_CONTRACT_ID preimage, computed here rather than
// by the library) and fetches that contract's instance. The executable the
// network stores must be the one the rendering names. A wrong deployer, salt
// or hash finds no contract, or a different executable.
func checkCreate(ctx context.Context, a explain.Action, c LiveCheck, status string) LiveCheck {
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
			NetworkId: xdr.Hash(sha256.Sum256([]byte(network.TestNetworkPassphrase))),
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
	key, err := xdr.MarshalBase64(xdr.LedgerKey{Type: xdr.LedgerEntryTypeContractData, ContractData: &xdr.LedgerKeyContractData{
		Contract:   xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &id},
		Key:        xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance},
		Durability: xdr.ContractDataDurabilityPersistent,
	}})
	if err != nil {
		c.Result, c.Detail = "check-error", err.Error()
		return c
	}
	var resp struct {
		Entries []struct {
			XDR string `json:"xdr"`
		} `json:"entries"`
	}
	if err := rpcCall(ctx, "getLedgerEntries", map[string]any{"keys": []string{key}}, &resp); err != nil {
		c.Result, c.Detail = "check-error", err.Error()
		return c
	}
	contract, _ := soroauth.FormatAddress(xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &id})
	if len(resp.Entries) != 1 {
		c.Result, c.Detail = "not-found", "no live instance at derived contract "+contract+" (it may have been archived)"
		return c
	}
	var data xdr.LedgerEntryData
	if err := xdr.SafeUnmarshalBase64(resp.Entries[0].XDR, &data); err != nil {
		c.Result, c.Detail = "check-error", err.Error()
		return c
	}
	inst, ok := data.MustContractData().Val.GetInstance()
	if !ok || inst.Executable.WasmHash == nil {
		c.Result, c.Detail = "ledger-mismatch", "instance at "+contract+" is not a wasm contract"
		return c
	}
	onLedger := "wasm " + hex.EncodeToString(inst.Executable.WasmHash[:])
	if onLedger != exe {
		c.Result, c.Detail = "ledger-mismatch", fmt.Sprintf("contract %s runs %s on the ledger, rendering says %s", contract, onLedger, exe)
		return c
	}
	c.Result = "ledger-match"
	c.Detail = fmt.Sprintf("contract %s, derived from deployer %s and salt %s, exists and runs %s", contract, deployer, salt, onLedger)
	return c
}
