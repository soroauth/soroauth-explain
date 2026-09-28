package explain

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	soroauth "github.com/soroauth/soroauth-go"
	"github.com/stellar/go-stellar-sdk/xdr"
)

const (
	testnetPassphrase = "Test SDF Network ; September 2015"
	publicPassphrase  = "Public Global Stellar Network ; September 2015"
)

// sacFixture is one getLedgerEntries response for a Stellar Asset Contract
// instance, recorded from a real network. See testdata/sac.
type sacFixture struct {
	Name              string `json:"name"`
	NetworkPassphrase string `json:"network_passphrase"`
	Response          struct {
		Result struct {
			Entries []struct {
				XDR string `json:"xdr"`
			} `json:"entries"`
		} `json:"result"`
	} `json:"response"`
}

// assetFromInstance reads the asset a Stellar Asset Contract instance stores
// under its AssetInfo key: [sym(Native)] or [sym(AlphaNum4|AlphaNum12),
// {asset_code: string, issuer: bytes}].
func assetFromInstance(t testing.TB, inst xdr.ScContractInstance) xdr.Asset {
	t.Helper()
	for _, e := range *inst.Storage {
		key, ok := e.Key.GetVec()
		if !ok || key == nil || len(*key) != 1 || (*key)[0].Type != xdr.ScValTypeScvSymbol || *(*key)[0].Sym != "AssetInfo" {
			continue
		}
		val := *e.Val.MustVec()
		switch *val[0].Sym {
		case "Native":
			return xdr.MustNewNativeAsset()
		case "AlphaNum4", "AlphaNum12":
			var code string
			var issuer []byte
			for _, f := range *val[1].MustMap() {
				switch *f.Key.Sym {
				case "asset_code":
					code = string(*f.Val.Str)
				case "issuer":
					issuer = *f.Val.Bytes
				}
			}
			var key [32]byte
			copy(key[:], issuer)
			g, err := soroauth.FormatAddress(accountAddress(key))
			if err != nil {
				t.Fatal(err)
			}
			return xdr.MustNewCreditAsset(code, g)
		}
		t.Fatalf("unrecognised AssetInfo %s", *val[0].Sym)
	}
	t.Fatal("no AssetInfo in instance storage")
	return xdr.Asset{}
}

// TestSACContractIDMatchesNetwork derives each recorded contract's ID from
// the asset in its own on-chain AssetInfo and the network passphrase, and
// compares it to the ID the network stored it under. No ID literal is trusted.
func TestSACContractIDMatchesNetwork(t *testing.T) {
	files, err := filepath.Glob("testdata/sac/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 4 {
		t.Fatalf("found %d SAC fixtures, want at least 4", len(files))
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var fx sacFixture
			if err := json.Unmarshal(raw, &fx); err != nil {
				t.Fatal(err)
			}
			if len(fx.Response.Result.Entries) != 1 {
				t.Fatalf("%d entries, want 1", len(fx.Response.Result.Entries))
			}
			var data xdr.LedgerEntryData
			if err := xdr.SafeUnmarshalBase64(fx.Response.Result.Entries[0].XDR, &data); err != nil {
				t.Fatal(err)
			}
			cd := data.MustContractData()
			inst := cd.Val.MustInstance()
			if inst.Executable.Type != xdr.ContractExecutableTypeContractExecutableStellarAsset {
				t.Fatalf("executable = %s, want a Stellar Asset Contract", inst.Executable.Type)
			}
			onChain, err := soroauth.FormatAddress(cd.Contract)
			if err != nil {
				t.Fatal(err)
			}
			asset := assetFromInstance(t, inst)

			derived, err := SACContractID(asset, fx.NetworkPassphrase)
			if err != nil {
				t.Fatal(err)
			}
			if derived != onChain {
				t.Fatalf("derived %s, network stores the SAC at %s", derived, onChain)
			}
			label, ok := AssetLabel(onChain, fx.NetworkPassphrase, []xdr.Asset{asset})
			if !ok {
				t.Fatalf("no label for the real SAC %s", onChain)
			}
			t.Logf("%s on %q: %s = %s", fx.Name, fx.NetworkPassphrase, onChain, label)
		})
	}
}

func TestSACContractIDNoNetwork(t *testing.T) {
	_, err := SACContractID(xdr.MustNewNativeAsset(), "")
	if !errors.Is(err, ErrUnknownNetwork) {
		t.Fatalf("err = %v, want ErrUnknownNetwork", err)
	}
}

func TestSACContractIDMalformedAsset(t *testing.T) {
	// A credit type with no code arm: an error, never a panic.
	_, err := SACContractID(xdr.Asset{Type: xdr.AssetTypeAssetTypeCreditAlphanum4}, testnetPassphrase)
	if !errors.Is(err, ErrEmptyArm) {
		t.Fatalf("err = %v, want ErrEmptyArm", err)
	}
	_, err = SACContractID(xdr.Asset{Type: xdr.AssetTypeAssetTypeCreditAlphanum12}, testnetPassphrase)
	if !errors.Is(err, ErrEmptyArm) {
		t.Fatalf("err = %v, want ErrEmptyArm", err)
	}
	_, err = SACContractID(xdr.Asset{Type: xdr.AssetType(9)}, testnetPassphrase)
	if err == nil {
		t.Fatal("unknown asset type derived an ID")
	}
	if _, ok := AssetLabel("C", testnetPassphrase, []xdr.Asset{{Type: xdr.AssetTypeAssetTypeCreditAlphanum4}}); ok {
		t.Fatal("malformed candidate produced a label")
	}
}

func mustSAC(t testing.TB, a xdr.Asset, passphrase string) string {
	t.Helper()
	id, err := SACContractID(a, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

const (
	usdcIssuerGA5Z = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
	usdcIssuerGAHK = "GAHKEAKBDDC467S3PFXPROVU6SBPQXDEWLUUTCPIIGD5MIO3KRRWS5HV"
)

func TestAssetLabel(t *testing.T) {
	native := xdr.MustNewNativeAsset()
	usdcA := xdr.MustNewCreditAsset("USDC", usdcIssuerGA5Z)
	usdcB := xdr.MustNewCreditAsset("USDC", usdcIssuerGAHK)
	nativeTestnet := mustSAC(t, native, testnetPassphrase)
	nativePublic := mustSAC(t, native, publicPassphrase)

	tests := []struct {
		name       string
		contract   string
		passphrase string
		candidates []xdr.Asset
		want       string
		wantOK     bool
	}{
		{"native_on_testnet", nativeTestnet, testnetPassphrase, []xdr.Asset{native}, "native", true},
		{"native_on_public", nativePublic, publicPassphrase, []xdr.Asset{native}, "native", true},
		{"public_sac_explained_on_testnet", nativePublic, testnetPassphrase, []xdr.Asset{native}, "", false},
		{"no_passphrase", nativeTestnet, "", []xdr.Asset{native}, "", false},
		{"no_candidates", nativeTestnet, testnetPassphrase, nil, "", false},
		{"issuer_is_in_the_label_a", mustSAC(t, usdcA, publicPassphrase), publicPassphrase, []xdr.Asset{usdcB, usdcA}, "USDC:" + usdcIssuerGA5Z, true},
		{"issuer_is_in_the_label_b", mustSAC(t, usdcB, publicPassphrase), publicPassphrase, []xdr.Asset{usdcA, usdcB}, "USDC:" + usdcIssuerGAHK, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := AssetLabel(tt.contract, tt.passphrase, tt.candidates)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("AssetLabel = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// TestAssetLabelImpostor is the impostor guard at the labelling layer.
//
// The impostor is a real Stellar Asset Contract for an issued asset with code
// "XLM". On-chain its symbol() reports "XLM" and its transfer has exactly the
// token signature, because it is a SAC; its ID simply does not derive from
// the native asset. It must never be labelled as the native asset, and no
// label derived from anything but its ID may appear.
func TestAssetLabelImpostor(t *testing.T) {
	fakeXLM := xdr.MustNewCreditAsset("XLM", usdcIssuerGAHK)
	impostors := map[string]string{
		"sac_of_issued_XLM":             mustSAC(t, fakeXLM, testnetPassphrase),
		"native_sac_of_another_network": mustSAC(t, xdr.MustNewNativeAsset(), publicPassphrase),
		"arbitrary_contract":            "CABAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAFNSZ",
	}
	for _, name := range []string{"sac_of_issued_XLM", "native_sac_of_another_network", "arbitrary_contract"} {
		contract := impostors[name]
		t.Run(name, func(t *testing.T) {
			label, ok := AssetLabel(contract, testnetPassphrase, []xdr.Asset{xdr.MustNewNativeAsset()})
			if ok || label != "" {
				t.Fatalf("impostor %s labelled %q", contract, label)
			}
		})
	}

	// Even when the caller offers the impostor's own asset as a candidate,
	// the label names its issuer and so cannot pass for the native asset.
	label, ok := AssetLabel(impostors["sac_of_issued_XLM"], testnetPassphrase, []xdr.Asset{fakeXLM})
	if !ok || label != "XLM:"+usdcIssuerGAHK || !strings.Contains(label, ":") {
		t.Fatalf("issued XLM labelled (%q, %v), want XLM:%s", label, ok, usdcIssuerGAHK)
	}
}
