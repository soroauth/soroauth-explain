//go:build live

package explain_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	explain "github.com/soroauth/soroauth-explain"
	"github.com/soroauth/soroauth-explain/internal/snapshot"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// liveEnvelopeHashes are the real transactions recorded in
// testdata/live/envelopes.json, chosen on 2026-11-02 from a scan of recent
// testnet and public ledgers to cover each envelope and host-function shape
// seen: an upload, both creation forms, a fee bump around a token transfer on
// each network, and two address-credential entries in one operation.
var liveEnvelopeHashes = []struct {
	net  liveNetwork
	hash string
}{
	{liveTestnet, "7a3331dd3ff3cd32958fdb12454e1c79ed3b251c5eb40a3baf4b1f1046350c82"},
	{liveTestnet, "c839b160d008b8500626e012a8c54bf6766a77124ce8bf3a1462948eb22ddb7a"},
	{liveTestnet, "2fc3446fd4ee8327b24ee95ea8caf9fbc13cbb76b7e6657bb211279b14d4ce05"},
	{liveTestnet, "d0cfc979388c97a24be8d96be693b63c741be5a84eae7a480d4854bc7d4b185b"},
	{liveTestnet, "639c9a8e53b7434ea31969dd91da0429fdc11ed849ccbced5ef177167da37c5f"},
	{livePublic, "d118280cea8b794f0558e078b0485d8a63c81b9b9262da4d74781418fcb90d71"},
}

// TestLiveEnvelopes fetches the recorded transactions. With
// SOROAUTH_LIVE_RECORD=1 it writes testdata/live/envelopes.json:
//
//	SOROAUTH_LIVE_RECORD=1 go test -tags live -run TestLiveEnvelopes -v -count=1 .
//
// Without it, it checks each recorded envelope is byte for byte what the
// network returns, while the RPC still retains it. TestEnvelopeFixtures
// checks offline that each envelope hashes to its transaction hash.
func TestLiveEnvelopes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	record := os.Getenv("SOROAUTH_LIVE_RECORD") == "1"
	var file EnvelopeFixtures
	if !record {
		raw, err := os.ReadFile(envelopeFixturesPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &file); err != nil {
			t.Fatal(err)
		}
	} else {
		file.Fetched = time.Now().UTC().Format("2006-01-02")
	}

	for i, c := range liveEnvelopeHashes {
		var res struct {
			Status      string `json:"status"`
			Ledger      uint32 `json:"ledger"`
			EnvelopeXdr string `json:"envelopeXdr"`
		}
		if err := rpcCall(ctx, c.net.rpc, "getTransaction", map[string]any{"hash": c.hash}, &res); err != nil {
			t.Fatalf("getTransaction %s: %v", c.hash, err)
		}
		if !record {
			if res.Status == "NOT_FOUND" {
				t.Logf("%s is no longer within %s's retention; the offline hash check still covers it", c.hash, c.net.rpc)
				continue
			}
			if got := file.Records[i]; got.TxHash != c.hash || got.EnvelopeXDR != res.EnvelopeXdr {
				t.Errorf("%s: recorded envelope differs from the network's", c.hash)
			}
			checkEnvelopeDecoded(ctx, t, c.net, fetchTx(ctx, t, c.net, c.hash), file.Records[i].EventAssets)
			continue
		}
		if res.Status == "NOT_FOUND" {
			t.Fatalf("%s: not found on %s", c.hash, c.net.rpc)
		}
		tx := fetchTx(ctx, t, c.net, c.hash)
		var events []xdr.ContractEvent
		for _, op := range tx.Events.ContractEventsXdr {
			for _, e := range op {
				var ev xdr.ContractEvent
				if err := xdr.SafeUnmarshalBase64(e, &ev); err != nil {
					t.Fatal(err)
				}
				events = append(events, ev)
			}
		}
		labels, _ := eventAssets(events)
		checkEnvelopeDecoded(ctx, t, c.net, tx, labels)
		file.Records = append(file.Records, EnvelopeFixture{
			Network: c.net.name, Passphrase: c.net.passphrase, RPC: c.net.rpc,
			TxHash: c.hash, Ledger: res.Ledger, TxStatus: res.Status,
			EventAssets: labels, EnvelopeXDR: res.EnvelopeXdr,
		})
	}

	if record {
		raw, err := json.MarshalIndent(file, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(envelopeFixturesPath, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("recorded %d envelopes to %s", len(file.Records), envelopeFixturesPath)
	}
}

// checkEnvelopeDecoded explains a transaction with its own event assets as
// candidates and checks every decoded action, in the invocation and in each
// entry, against the host's contract events or ledger state, through the
// same checkDecoded the entry records use. A check that contradicts a
// decoded rendering fails the test.
func checkEnvelopeDecoded(ctx context.Context, t *testing.T, net liveNetwork, tx rpcTx, labels []string) {
	t.Helper()
	var env xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(tx.EnvelopeXdr, &env); err != nil {
		t.Fatal(err)
	}
	assets, err := snapshot.ParseAssets(labels)
	if err != nil {
		t.Fatal(err)
	}
	exp, err := explain.ExplainEnvelope(env, explain.WithNetwork(net.passphrase), explain.WithAssets(assets...))
	if err != nil {
		t.Fatalf("%s: %v", tx.TxHash, err)
	}
	created := createdInstances(t, tx)
	for _, op := range exp.Operations {
		var events []xdr.ContractEvent
		if op.Index < len(tx.Events.ContractEventsXdr) {
			for _, e := range tx.Events.ContractEventsXdr[op.Index] {
				var ev xdr.ContractEvent
				if err := xdr.SafeUnmarshalBase64(e, &ev); err != nil {
					t.Fatal(err)
				}
				events = append(events, ev)
			}
		}
		var checks []LiveCheck
		if op.Invocation != nil {
			checks = append(checks, checkDecoded(ctx, net, []explain.Action{*op.Invocation}, "invocation.", tx.Status, events, created)...)
		}
		for i, e := range op.Entries {
			checks = append(checks, checkDecoded(ctx, net, e.Actions, fmt.Sprintf("entry%d.", i), tx.Status, events, created)...)
		}
		for _, c := range checks {
			t.Logf("%s op %d %s %s: %s (%s)", tx.TxHash[:12], op.Index, c.Path, c.Kind, c.Result, c.Detail)
			switch c.Result {
			case "no-matching-event", "asset-mismatch", "ledger-mismatch", "check-error":
				t.Errorf("%s op %d %s: decoded rendering contradicted: %s", tx.TxHash, op.Index, c.Path, c.Detail)
			}
		}
	}
}
