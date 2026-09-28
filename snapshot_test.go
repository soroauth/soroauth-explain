package explain_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	explain "github.com/soroauth/soroauth-explain"
	"github.com/soroauth/soroauth-explain/internal/snapshot"
	"github.com/soroauth/soroauth-explain/render"
	soroauth "github.com/soroauth/soroauth-go"
	"github.com/stellar/go-stellar-sdk/xdr"
)

const (
	entriesDir   = "testdata/entries"
	snapshotsDir = "testdata/snapshots"
	testnet      = "Test SDF Network ; September 2015"
)

// TestSnapshots is the correctness gate. Every committed input is rendered
// in every format and compared byte for byte with its committed snapshot.
// Snapshots are never edited by hand: when output changes, regenerate with
// `go run ./cmd/gensnapshots` and explain the change in the commit body.
func TestSnapshots(t *testing.T) {
	entries, err := snapshot.LoadEntries(entriesDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no snapshot inputs found")
	}
	for _, e := range entries {
		t.Run(e.Name, func(t *testing.T) {
			got, err := snapshot.Render(e)
			if err != nil {
				t.Fatal(err)
			}
			for _, format := range snapshot.Formats {
				want, err := snapshot.Read(snapshotsDir, e.Name, format)
				if err != nil {
					t.Fatalf("%v (regenerate with `go run ./cmd/gensnapshots`)", err)
				}
				if !bytes.Equal(got[format], want) {
					t.Errorf("%s.%s differs from its snapshot at %s\n--- got ---\n%s\n--- want (committed) ---\n%s",
						e.Name, format, firstDifference(got[format], want), got[format], want)
				}
			}
		})
	}
}

// firstDifference names the first line on which two renderings differ.
func firstDifference(got, want []byte) string {
	g := strings.Split(string(got), "\n")
	w := strings.Split(string(want), "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			return "line " + strconv.Itoa(i+1) + ":\n  got:  " + gl + "\n  want: " + wl
		}
	}
	return "end of file"
}

// TestSnapshotsHaveInputs fails on a snapshot with no input, so a removed
// case cannot leave a stale rendering behind.
func TestSnapshotsHaveInputs(t *testing.T) {
	entries, err := snapshot.LoadEntries(entriesDir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, e := range entries {
		for _, f := range snapshot.Formats {
			want[e.Name+"."+f] = true
		}
	}
	files, err := os.ReadDir(snapshotsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if !want[f.Name()] {
			t.Errorf("snapshot %s has no input in %s", f.Name(), entriesDir)
		}
	}
	if len(files) != len(want) {
		t.Errorf("%d snapshot files, want %d", len(files), len(want))
	}
}

func TestSnapshotMissing(t *testing.T) {
	_, err := snapshot.Read(snapshotsDir, "no_such_case", "txt")
	if !errors.Is(err, explain.ErrNoSnapshot) {
		t.Fatalf("err = %v, want ErrNoSnapshot", err)
	}
}

// TestSnapshotInputsDecode proves every input is consumed: each decodes to
// an entry and yields a rendering in every format.
func TestSnapshotInputsDecode(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(entriesDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := snapshot.LoadEntries(entriesDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(files) {
		t.Fatalf("loaded %d of %d input files", len(entries), len(files))
	}
}

func account(b byte) xdr.ScAddress {
	var k xdr.Uint256
	for i := range k {
		k[i] = b
	}
	return xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeAccount,
		AccountId: &xdr.AccountId{Type: xdr.PublicKeyTypePublicKeyTypeEd25519, Ed25519: &k}}
}

func transferEntry(t testing.TB, contract string) xdr.SorobanAuthorizationEntry {
	t.Helper()
	c, err := soroauth.ParseAddress(contract)
	if err != nil {
		t.Fatal(err)
	}
	from, to := account(0x22), account(0x33)
	return xdr.SorobanAuthorizationEntry{
		Credentials: xdr.SorobanCredentials{
			Type: xdr.SorobanCredentialsTypeSorobanCredentialsAddress,
			Address: &xdr.SorobanAddressCredentials{
				Address: account(0x11), Nonce: 7, SignatureExpirationLedger: 1000,
				Signature: xdr.ScVal{Type: xdr.ScValTypeScvVoid},
			},
		},
		RootInvocation: xdr.SorobanAuthorizedInvocation{Function: xdr.SorobanAuthorizedFunction{
			Type: xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn,
			ContractFn: &xdr.InvokeContractArgs{
				ContractAddress: c,
				FunctionName:    "transfer",
				Args: []xdr.ScVal{
					{Type: xdr.ScValTypeScvAddress, Address: &from},
					{Type: xdr.ScValTypeScvAddress, Address: &to},
					{Type: xdr.ScValTypeScvI128, I128: &xdr.Int128Parts{Lo: 1000000000}},
				},
			},
		}},
	}
}

// assetFieldLine matches the text renderer's line for a field named asset.
var assetFieldLine = regexp.MustCompile(`(?m)^\s+asset\s+= `)

func renderAll(t testing.TB, entry xdr.SorobanAuthorizationEntry) map[string]string {
	t.Helper()
	exp, err := explain.Explain(entry, explain.WithNetwork(testnet))
	if err != nil {
		t.Fatal(err)
	}
	js, err := render.JSON(exp)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{"text": render.Text(exp), "json": string(js)}
}

// TestImpostorNeverLabelled is the impostor guard end to end, in every output
// format.
//
// The impostor is the testnet Stellar Asset Contract of an issued asset coded
// "XLM". It implements transfer with exactly the token signature and its
// symbol() reports "XLM", because it is a real SAC; the call below uses that
// signature. Its ID does not derive from the native asset, so no output may
// carry an asset label for it: it renders as its strkey address and nothing
// more. The native SAC is the control, proving the label path is live.
func TestImpostorNeverLabelled(t *testing.T) {
	fakeXLM := xdr.MustNewCreditAsset("XLM", "GAHKEAKBDDC467S3PFXPROVU6SBPQXDEWLUUTCPIIGD5MIO3KRRWS5HV")
	impostor, err := explain.SACContractID(fakeXLM, testnet)
	if err != nil {
		t.Fatal(err)
	}
	native, err := explain.SACContractID(xdr.MustNewNativeAsset(), testnet)
	if err != nil {
		t.Fatal(err)
	}

	for format, out := range renderAll(t, transferEntry(t, impostor)) {
		t.Run("impostor_"+format, func(t *testing.T) {
			if !strings.Contains(out, impostor) {
				t.Errorf("impostor contract %s not shown as its address", impostor)
			}
			// No label, no asset field, and no scaled amount: scaling would
			// claim a decimal count the impostor has not earned.
			for _, label := range []string{"native", "XLM", "Stellar Asset Contract", `"name": "asset"`, "100.0000000"} {
				if strings.Contains(out, label) {
					t.Errorf("impostor rendering contains %q:\n%s", label, out)
				}
			}
			if assetFieldLine.MatchString(out) {
				t.Errorf("impostor text rendering has an asset field:\n%s", out)
			}
		})
	}

	for format, out := range renderAll(t, transferEntry(t, native)) {
		t.Run("control_"+format, func(t *testing.T) {
			if !strings.Contains(out, "native") {
				t.Errorf("real native SAC not labelled; the guard test would pass vacuously:\n%s", out)
			}
		})
	}

	// The committed impostor snapshot must hold the same line.
	for _, format := range snapshot.Formats {
		b, err := snapshot.Read(snapshotsDir, "built_impostor_transfer", format)
		if err != nil {
			t.Fatal(err)
		}
		for _, label := range []string{"native", "XLM", "Stellar Asset Contract", `"name": "asset"`, "100.0000000"} {
			if bytes.Contains(b, []byte(label)) {
				t.Errorf("committed impostor snapshot .%s contains %q", format, label)
			}
		}
	}
}
