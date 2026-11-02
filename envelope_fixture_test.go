package explain_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// envelopeFixturesPath holds real transaction envelopes, recorded by
// TestLiveEnvelopes (build tag live). cmd/gensnapshots builds snapshot cases
// from them.
const envelopeFixturesPath = "testdata/live/envelopes.json"

// EnvelopeFixtures is the file at envelopeFixturesPath.
type EnvelopeFixtures struct {
	Fetched string            `json:"fetched"`
	Records []EnvelopeFixture `json:"records"`
}

// EnvelopeFixture is one recorded transaction.
type EnvelopeFixture struct {
	Network     string   `json:"network"`
	Passphrase  string   `json:"network_passphrase"`
	RPC         string   `json:"rpc"`
	TxHash      string   `json:"tx_hash"`
	Ledger      uint32   `json:"ledger"`
	TxStatus    string   `json:"tx_status"`
	EventAssets []string `json:"event_assets,omitempty"`
	EnvelopeXDR string   `json:"envelope_xdr"`
}

// TestEnvelopeFixtures proves offline that every recorded envelope is the
// transaction its hash names: the hash is recomputed from the envelope and
// the network passphrase with the SDK's own network.HashTransactionInEnvelope.
// A fixture edited by hand, or recorded against the wrong network, fails.
func TestEnvelopeFixtures(t *testing.T) {
	raw, err := os.ReadFile(envelopeFixturesPath)
	if err != nil {
		t.Fatal(err)
	}
	var file EnvelopeFixtures
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Records) == 0 {
		t.Fatal("no recorded envelopes")
	}
	for _, r := range file.Records {
		t.Run(r.Network+"/"+r.TxHash[:12], func(t *testing.T) {
			var env xdr.TransactionEnvelope
			if err := xdr.SafeUnmarshalBase64(r.EnvelopeXDR, &env); err != nil {
				t.Fatal(err)
			}
			h, err := network.HashTransactionInEnvelope(env, r.Passphrase)
			if err != nil {
				t.Fatal(err)
			}
			if got := hex.EncodeToString(h[:]); got != r.TxHash {
				t.Errorf("envelope hashes to %s, recorded as %s", got, r.TxHash)
			}
		})
	}
}
