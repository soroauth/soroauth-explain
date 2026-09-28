// Command gen regenerates testdata/entries and testdata/snapshots.
//
// Run it from the module root with `go run ./testdata/gen`. It refuses to run
// with a dirty working tree, so a regeneration can only ever change what the
// code change behind it changed, and nothing smuggled in beside it.
//
// Inputs come from two places. Every entry in soroauth-go's golden vectors is
// copied from the soroauth-go module at the exact version go.mod pins, read
// from the module cache, whose contents go.sum verifies. The cases those
// vectors lack are built here, deterministically, by the functions below.
// Snapshots are then rendered for every input through internal/snapshot, the
// same code the gate in snapshot_test.go uses.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	explain "github.com/soroauth/soroauth-explain"
	"github.com/soroauth/soroauth-explain/internal/snapshot"
	"github.com/stellar/go-stellar-sdk/xdr"
)

const (
	entriesDir   = "testdata/entries"
	snapshotsDir = "testdata/snapshots"
	soroauthMod  = "github.com/soroauth/soroauth-go"

	testnet = "Test SDF Network ; September 2015"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run() error {
	if _, err := os.Stat("go.mod"); err != nil {
		return fmt.Errorf("run from the module root: %w", err)
	}
	status, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	if len(strings.TrimSpace(string(status))) != 0 {
		return fmt.Errorf("refusing to run with a dirty working tree; commit or stash first:\n%s", status)
	}

	entries, err := copiedEntries()
	if err != nil {
		return err
	}
	built, err := builtEntries()
	if err != nil {
		return err
	}
	entries = append(entries, built...)

	// Rewrite both directories from scratch so a removed case leaves no
	// orphaned file behind.
	for _, dir := range []string{entriesDir, snapshotsDir} {
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	for _, e := range entries {
		raw, err := json.MarshalIndent(e, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(entriesDir, e.Name+".json"), append(raw, '\n'), 0o644); err != nil {
			return err
		}
		out, err := snapshot.Render(e)
		if err != nil {
			return err
		}
		for _, format := range snapshot.Formats {
			if err := os.WriteFile(snapshot.Path(snapshotsDir, e.Name, format), out[format], 0o644); err != nil {
				return err
			}
		}
	}
	fmt.Printf("gen: wrote %d entries and %d snapshots\n", len(entries), len(entries)*len(snapshot.Formats))
	return nil
}

// copiedEntries reads soroauth-go's golden vectors at the pinned version.
// Each vector yields a case per entry form it carries.
func copiedEntries() ([]snapshot.Entry, error) {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Version}} {{.Dir}}", soroauthMod).Output()
	if err != nil {
		return nil, fmt.Errorf("locate %s: %w", soroauthMod, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 || fields[1] == "" {
		return nil, fmt.Errorf("locate %s: %q (run go mod download)", soroauthMod, out)
	}
	version, dir := fields[0], fields[1]

	files, err := filepath.Glob(filepath.Join(dir, "testdata", "vectors", "*.json"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no vectors under %s", dir)
	}
	var entries []snapshot.Entry
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var v struct {
			Name              string `json:"name"`
			NetworkPassphrase string `json:"network_passphrase"`
			PreWrap           string `json:"pre_wrap_entry_xdr"`
			Unsigned          string `json:"unsigned_entry_xdr"`
			Signed            string `json:"signed_entry_xdr"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		for _, form := range []struct{ field, suffix, xdr string }{
			{"pre_wrap_entry_xdr", "pre_wrap", v.PreWrap},
			{"unsigned_entry_xdr", "unsigned", v.Unsigned},
			{"signed_entry_xdr", "signed", v.Signed},
		} {
			if form.xdr == "" {
				continue
			}
			entries = append(entries, snapshot.Entry{
				Name:              v.Name + "." + form.suffix,
				Source:            fmt.Sprintf("%s@%s testdata/vectors/%s %s", soroauthMod, version, filepath.Base(f), form.field),
				NetworkPassphrase: v.NetworkPassphrase,
				EntryXDR:          form.xdr,
			})
		}
	}
	return entries, nil
}

// Fixed keys for built cases. None is a real account; each is 32 repeated
// bytes so that it is recognisable in a rendering.
var (
	subjectKey = key(0x11)
	fromKey    = key(0x22)
	toKey      = key(0x33)
	opaqueKey  = key(0x44)
)

// impostorIssuer issues the asset coded "XLM" whose Stellar Asset Contract
// is the impostor. Any account works; this is a fixed arbitrary one.
const impostorIssuer = "GAHKEAKBDDC467S3PFXPROVU6SBPQXDEWLUUTCPIIGD5MIO3KRRWS5HV"

func key(b byte) [32]byte {
	var k [32]byte
	for i := range k {
		k[i] = b
	}
	return k
}

func account(k [32]byte) xdr.ScAddress {
	u := xdr.Uint256(k)
	return xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeAccount,
		AccountId: &xdr.AccountId{Type: xdr.PublicKeyTypePublicKeyTypeEd25519, Ed25519: &u}}
}

func contract(k [32]byte) xdr.ScAddress {
	id := xdr.ContractId(k)
	return xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &id}
}

func sacAddress(a xdr.Asset, passphrase string) (xdr.ScAddress, error) {
	id, err := a.ContractID(passphrase)
	if err != nil {
		return xdr.ScAddress{}, err
	}
	return contract(id), nil
}

func addr(a xdr.ScAddress) xdr.ScVal { return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &a} }

func i128(n uint64) xdr.ScVal {
	return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &xdr.Int128Parts{Lo: xdr.Uint64(n)}}
}

func u32(n uint32) xdr.ScVal {
	v := xdr.Uint32(n)
	return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &v}
}

func call(c xdr.ScAddress, fn string, args []xdr.ScVal, subs ...xdr.SorobanAuthorizedInvocation) xdr.SorobanAuthorizedInvocation {
	return xdr.SorobanAuthorizedInvocation{
		Function: xdr.SorobanAuthorizedFunction{
			Type:       xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn,
			ContractFn: &xdr.InvokeContractArgs{ContractAddress: c, FunctionName: xdr.ScSymbol(fn), Args: args},
		},
		SubInvocations: subs,
	}
}

func addressEntry(root xdr.SorobanAuthorizedInvocation) xdr.SorobanAuthorizationEntry {
	return xdr.SorobanAuthorizationEntry{
		Credentials: xdr.SorobanCredentials{
			Type: xdr.SorobanCredentialsTypeSorobanCredentialsAddress,
			Address: &xdr.SorobanAddressCredentials{
				Address:                   account(subjectKey),
				Nonce:                     7,
				SignatureExpirationLedger: 1000,
				Signature:                 xdr.ScVal{Type: xdr.ScValTypeScvVoid},
			},
		},
		RootInvocation: root,
	}
}

// chain nests n calls, each the only sub-invocation of the one above.
func chain(n int) xdr.SorobanAuthorizedInvocation {
	inv := call(contract(opaqueKey), "level_"+fmt.Sprint(n), nil)
	for i := n - 1; i >= 1; i-- {
		inv = call(contract(opaqueKey), "level_"+fmt.Sprint(i), nil, inv)
	}
	return inv
}

func builtEntries() ([]snapshot.Entry, error) {
	transferArgs := []xdr.ScVal{addr(account(fromKey)), addr(account(toKey)), i128(1000000000)}

	impostor, err := sacAddress(xdr.MustNewCreditAsset("XLM", impostorIssuer), testnet)
	if err != nil {
		return nil, err
	}
	native, err := sacAddress(xdr.MustNewNativeAsset(), testnet)
	if err != nil {
		return nil, err
	}

	hundred := make([]xdr.ScVal, 100)
	for i := range hundred {
		hundred[i] = u32(uint32(i))
	}

	cases := []struct {
		name, how string
		entry     xdr.SorobanAuthorizationEntry
	}{
		{"built_impostor_transfer",
			"transfer(from, to, i128 1000000000) on the testnet Stellar Asset Contract of XLM:" + impostorIssuer + ", an issued asset coded XLM, not the native asset",
			addressEntry(call(impostor, "transfer", transferArgs))},
		{"built_native_sac_transfer",
			"the same transfer on the testnet native Stellar Asset Contract: the positive control for the impostor case",
			addressEntry(call(native, "transfer", transferArgs))},
		{"built_unknown_function",
			"do_thing(address, i128, bytes) on an arbitrary contract",
			addressEntry(call(contract(opaqueKey), "do_thing", []xdr.ScVal{
				addr(account(toKey)), i128(42), {Type: xdr.ScValTypeScvBytes, Bytes: &xdr.ScBytes{0xca, 0xfe}}}))},
		{"built_hundred_arguments",
			"a call with 100 u32 arguments",
			addressEntry(call(contract(opaqueKey), "wide", hundred))},
		{"built_deep_tree",
			fmt.Sprintf("a chain of %d nested invocations, exactly the default depth limit", explain.DefaultMaxDepth),
			addressEntry(chain(explain.DefaultMaxDepth))},
		{"built_deep_tree_over_limit",
			fmt.Sprintf("a chain of %d nested invocations, one past the default depth limit", explain.DefaultMaxDepth+1),
			addressEntry(chain(explain.DefaultMaxDepth + 1))},
	}

	entries := make([]snapshot.Entry, 0, len(cases))
	for _, c := range cases {
		b64, err := xdr.MarshalBase64(c.entry)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.name, err)
		}
		entries = append(entries, snapshot.Entry{
			Name:              c.name,
			Source:            "built by testdata/gen: " + c.how,
			NetworkPassphrase: testnet,
			EntryXDR:          b64,
		})
	}
	return entries, nil
}
