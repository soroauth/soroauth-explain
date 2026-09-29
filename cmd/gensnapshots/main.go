// Command gensnapshots regenerates testdata/entries and testdata/snapshots.
//
// Run it from the module root with `go run ./cmd/gensnapshots`. It refuses to run
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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	explain "github.com/soroauth/soroauth-explain"
	"github.com/soroauth/soroauth-explain/internal/snapshot"
	"github.com/soroauth/soroauth-explain/spec"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

const (
	entriesDir   = "testdata/entries"
	snapshotsDir = "testdata/snapshots"
	soroauthMod  = "github.com/soroauth/soroauth-go"

	testnet = "Test SDF Network ; September 2015"
	public  = "Public Global Stellar Network ; September 2015"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gensnapshots:", err)
		os.Exit(1)
	}
}

func run() error {
	if _, err := os.Stat("go.mod"); err != nil {
		return fmt.Errorf("run from the module root: %w", err)
	}
	if err := checkClean("."); err != nil {
		return err
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
	specCases, err := specEntries()
	if err != nil {
		return err
	}
	entries = append(entries, specCases...)

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
	fmt.Printf("gensnapshots: wrote %d entries and %d snapshots\n", len(entries), len(entries)*len(snapshot.Formats))
	return nil
}

// checkClean refuses a working tree with any modified, staged or untracked
// file, so a regeneration can only carry the change that caused it.
func checkClean(dir string) error {
	status, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	if len(strings.TrimSpace(string(status))) != 0 {
		return fmt.Errorf("refusing to run with a dirty working tree; commit or stash first:\n%s", status)
	}
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

// usdcIssuer issues the public-network USDC whose SAC is recorded in
// testdata/sac/usdc_ga5z_public.json.
const usdcIssuer = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

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

	usdc := xdr.MustNewCreditAsset("USDC", usdcIssuer)
	usdcSAC, err := sacAddress(usdc, public)
	if err != nil {
		return nil, err
	}
	lookalikeSAC, err := sacAddress(xdr.MustNewCreditAsset("USDC", impostorIssuer), public)
	if err != nil {
		return nil, err
	}
	usdcLabel := "USDC:" + usdcIssuer

	cases := []struct {
		name, how string
		network   string
		assets    []string
		entry     xdr.SorobanAuthorizationEntry
	}{
		{"built_usdc_transfer_with_asset",
			"transfer on the public-network SAC of " + usdcLabel + ", with that asset given as a candidate",
			public, []string{usdcLabel}, addressEntry(call(usdcSAC, "transfer", transferArgs))},
		{"built_usdc_transfer_without_asset",
			"the same transfer with no candidate assets: the token cannot be identified",
			public, nil, addressEntry(call(usdcSAC, "transfer", transferArgs))},
		{"built_usdc_lookalike_with_asset",
			"transfer on the public-network SAC of USDC:" + impostorIssuer + " (a different issuer), with " + usdcLabel + " as the only candidate",
			public, []string{usdcLabel}, addressEntry(call(lookalikeSAC, "transfer", transferArgs))},
		{"built_impostor_transfer",
			"transfer(from, to, i128 1000000000) on the testnet Stellar Asset Contract of XLM:" + impostorIssuer + ", an issued asset coded XLM, not the native asset",
			testnet, nil,
			addressEntry(call(impostor, "transfer", transferArgs))},
		{"built_native_sac_transfer",
			"the same transfer on the testnet native Stellar Asset Contract: the positive control for the impostor case",
			testnet, nil,
			addressEntry(call(native, "transfer", transferArgs))},
		{"built_unknown_function",
			"do_thing(address, i128, bytes) on an arbitrary contract",
			testnet, nil,
			addressEntry(call(contract(opaqueKey), "do_thing", []xdr.ScVal{
				addr(account(toKey)), i128(42), {Type: xdr.ScValTypeScvBytes, Bytes: &xdr.ScBytes{0xca, 0xfe}}}))},
		{"built_hundred_arguments",
			"a call with 100 u32 arguments",
			testnet, nil,
			addressEntry(call(contract(opaqueKey), "wide", hundred))},
		{"built_source_account",
			"a source-account entry over the unknown-function call; soroauth-go's vectors carry no source-account entry",
			testnet, nil,
			xdr.SorobanAuthorizationEntry{
				Credentials:    xdr.SorobanCredentials{Type: xdr.SorobanCredentialsTypeSorobanCredentialsSourceAccount},
				RootInvocation: call(contract(opaqueKey), "do_thing", nil),
			}},
		{"built_deep_tree",
			fmt.Sprintf("a chain of %d nested invocations, exactly the default depth limit", explain.DefaultMaxDepth),
			testnet, nil,
			addressEntry(chain(explain.DefaultMaxDepth))},
		{"built_deep_tree_over_limit",
			fmt.Sprintf("a chain of %d nested invocations, one past the default depth limit", explain.DefaultMaxDepth+1),
			testnet, nil,
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
			Source:            "built by cmd/gensnapshots: " + c.how,
			NetworkPassphrase: c.network,
			Assets:            c.assets,
			EntryXDR:          b64,
		})
	}
	return entries, nil
}

// oracleContract runs the code recorded in spec/testdata/oracle_testnet.wasm.
const oracleContract = "CAAL5BZAQ3W2G5CZX4HVLWNQIJISQ62ZNXXYCX2LONG4FFLRRYEYONZC"

func specFunction(name string, inputs ...xdr.ScSpecFunctionInputV0) xdr.ScSpecEntry {
	return xdr.ScSpecEntry{Kind: xdr.ScSpecEntryKindScSpecEntryFunctionV0, FunctionV0: &xdr.ScSpecFunctionV0{Name: xdr.ScSymbol(name), Inputs: inputs}}
}

func specInput(name string, typ xdr.ScSpecType) xdr.ScSpecFunctionInputV0 {
	return xdr.ScSpecFunctionInputV0{Name: name, Type: xdr.ScSpecTypeDef{Type: typ}}
}

func specSection(entries ...xdr.ScSpecEntry) (string, error) {
	var out []byte
	for _, e := range entries {
		b, err := e.MarshalBinary()
		if err != nil {
			return "", err
		}
		out = append(out, b...)
	}
	return base64.StdEncoding.EncodeToString(out), nil
}

// specEntries builds the cases for explain.WithSpecs. The real case is a
// set_price entry taken from the committed testnet live record, explained
// with the spec section of the recorded oracle wasm.
func specEntries() ([]snapshot.Entry, error) {
	wasm, err := os.ReadFile("spec/testdata/oracle_testnet.wasm")
	if err != nil {
		return nil, err
	}
	section, err := spec.Section(wasm)
	if err != nil {
		return nil, err
	}
	oracleSpec := base64.StdEncoding.EncodeToString(section)
	oracleSource := "spec section of spec/testdata/oracle_testnet.wasm, the testnet code of " + oracleContract

	raw, err := os.ReadFile("testdata/live/testnet.json")
	if err != nil {
		return nil, err
	}
	var run struct {
		Records []struct {
			TxHash    string `json:"tx_hash"`
			Operation int    `json:"operation"`
			AuthIndex int    `json:"auth_index"`
			EntryXDR  string `json:"entry_xdr"`
		} `json:"records"`
	}
	if err := json.Unmarshal(raw, &run); err != nil {
		return nil, err
	}
	var realXDR, realFrom string
	for _, r := range run.Records {
		var e xdr.SorobanAuthorizationEntry
		if err := xdr.SafeUnmarshalBase64(r.EntryXDR, &e); err != nil {
			return nil, err
		}
		fn, ok := e.RootInvocation.Function.GetContractFn()
		if !ok || string(fn.FunctionName) != "set_price" {
			continue
		}
		if c, err := strkey.Encode(strkey.VersionByteContract, fn.ContractAddress.ContractId[:]); err == nil && c == oracleContract {
			realXDR = r.EntryXDR
			realFrom = fmt.Sprintf("testnet tx %s operation %d entry %d, from testdata/live/testnet.json", r.TxHash, r.Operation, r.AuthIndex)
			break
		}
	}
	if realXDR == "" {
		return nil, fmt.Errorf("no set_price entry on %s in the testnet live record", oracleContract)
	}

	oracleRaw, err := strkey.Decode(strkey.VersionByteContract, oracleContract)
	if err != nil {
		return nil, err
	}
	oracle := contract([32]byte(oracleRaw))
	mismatch, err := xdr.MarshalBase64(addressEntry(call(oracle, "set_price", []xdr.ScVal{
		addr(account(fromKey)), {Type: xdr.ScValTypeScvSymbol, Sym: ptrSym("BTC")}, {Type: xdr.ScValTypeScvSymbol, Sym: ptrSym("high")}})))
	if err != nil {
		return nil, err
	}

	misleadingSpec, err := specSection(
		specFunction("do_thing", specInput("xlm_amount", xdr.ScSpecTypeScSpecTypeI128), specInput("recipient", xdr.ScSpecTypeScSpecTypeAddress)))
	if err != nil {
		return nil, err
	}
	misleading, err := xdr.MarshalBase64(addressEntry(call(contract(opaqueKey), "do_thing", []xdr.ScVal{i128(1000000000), addr(account(toKey))})))
	if err != nil {
		return nil, err
	}
	unplainSpec, err := specSection(specFunction("configure", specInput("limit\x1b[31m", xdr.ScSpecTypeScSpecTypeU32), specInput("window", xdr.ScSpecTypeScSpecTypeU32)))
	if err != nil {
		return nil, err
	}
	unplain, err := xdr.MarshalBase64(addressEntry(call(contract(opaqueKey), "configure", []xdr.ScVal{u32(5), u32(60)})))
	if err != nil {
		return nil, err
	}
	opaqueContract, err := strkey.Encode(strkey.VersionByteContract, opaqueKey[:])
	if err != nil {
		return nil, err
	}

	return []snapshot.Entry{
		{Name: "spec_set_price", Source: realFrom, NetworkPassphrase: testnet,
			Specs: map[string]string{oracleContract: oracleSpec}, SpecSource: oracleSource, EntryXDR: realXDR},
		{Name: "spec_type_mismatch", Source: "built by cmd/gensnapshots: set_price on the oracle with a symbol where its spec declares i128",
			NetworkPassphrase: testnet, Specs: map[string]string{oracleContract: oracleSpec}, SpecSource: oracleSource, EntryXDR: mismatch},
		{Name: "spec_misleading_names", Source: "built by cmd/gensnapshots: a spec naming arguments xlm_amount and recipient on an arbitrary contract",
			NetworkPassphrase: testnet, Specs: map[string]string{opaqueContract: misleadingSpec}, SpecSource: "built by cmd/gensnapshots", EntryXDR: misleading},
		{Name: "spec_unplain_name", Source: "built by cmd/gensnapshots: a spec whose argument name carries a terminal escape",
			NetworkPassphrase: testnet, Specs: map[string]string{opaqueContract: unplainSpec}, SpecSource: "built by cmd/gensnapshots", EntryXDR: unplain},
	}, nil
}

func ptrSym(s string) *xdr.ScSymbol {
	sym := xdr.ScSymbol(s)
	return &sym
}
