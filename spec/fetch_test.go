package spec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

func contractAddr(t testing.TB, b byte) (string, xdr.ScAddress) {
	t.Helper()
	var id xdr.ContractId
	for i := range id {
		id[i] = b
	}
	s, err := strkey.Encode(strkey.VersionByteContract, id[:])
	if err != nil {
		t.Fatal(err)
	}
	return s, xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &id}
}

// fakeRPC answers getLedgerEntries from a table of ledger keys to entries,
// and records how many requests it served.
type fakeRPC struct {
	entries  map[string]xdr.LedgerEntryData
	raw      map[string]string // key -> literal response body, overriding entries
	requests int
}

func (f *fakeRPC) put(t testing.TB, key xdr.LedgerKey, data xdr.LedgerEntryData) {
	t.Helper()
	k, err := xdr.MarshalBase64(key)
	if err != nil {
		t.Fatal(err)
	}
	f.entries[k] = data
}

func (f *fakeRPC) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests++
	var req struct {
		Method string `json:"method"`
		Params struct {
			Keys []string `json:"keys"`
		} `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Method != "getLedgerEntries" || len(req.Params.Keys) != 1 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	key := req.Params.Keys[0]
	if body, ok := f.raw[key]; ok {
		w.Write([]byte(body))
		return
	}
	var entries []map[string]string
	if data, ok := f.entries[key]; ok {
		b64, _ := xdr.MarshalBase64(data)
		entries = append(entries, map[string]string{"key": key, "xdr": b64})
	}
	json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"entries": entries}})
}

func instanceKey(addr xdr.ScAddress) xdr.LedgerKey {
	return xdr.LedgerKey{Type: xdr.LedgerEntryTypeContractData, ContractData: &xdr.LedgerKeyContractData{
		Contract: addr, Key: xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance}, Durability: xdr.ContractDataDurabilityPersistent,
	}}
}

func instance(addr xdr.ScAddress, exe xdr.ContractExecutable) xdr.LedgerEntryData {
	inst := xdr.ScContractInstance{Executable: exe}
	return xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeContractData, ContractData: &xdr.ContractDataEntry{
		Contract: addr, Key: xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance},
		Durability: xdr.ContractDataDurabilityPersistent,
		Val:        xdr.ScVal{Type: xdr.ScValTypeScvContractInstance, Instance: &inst},
	}}
}

func codeEntry(hash xdr.Hash, code []byte) xdr.LedgerEntryData {
	return xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeContractCode, ContractCode: &xdr.ContractCodeEntry{Hash: hash, Code: code}}
}

// world serves: a wasm contract running the recorded oracle code, a Stellar
// Asset Contract, a contract whose code entry does not hash to its instance's
// hash, and a contract with no instance at all.
func world(t *testing.T) (*fakeRPC, map[string]string) {
	t.Helper()
	f := &fakeRPC{entries: map[string]xdr.LedgerEntryData{}, raw: map[string]string{}}
	wasm := loadOracle(t)
	hash := xdr.Hash(sha256.Sum256(wasm))

	oracle, oracleAddr := contractAddr(t, 1)
	f.put(t, instanceKey(oracleAddr), instance(oracleAddr, xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableWasm, WasmHash: &hash}))
	f.put(t, xdr.LedgerKey{Type: xdr.LedgerEntryTypeContractCode, ContractCode: &xdr.LedgerKeyContractCode{Hash: hash}}, codeEntry(hash, wasm))

	sac, sacAddr := contractAddr(t, 2)
	f.put(t, instanceKey(sacAddr), instance(sacAddr, xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableStellarAsset}))

	tampered, tamperedAddr := contractAddr(t, 3)
	badHash := xdr.Hash{9}
	f.put(t, instanceKey(tamperedAddr), instance(tamperedAddr, xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableWasm, WasmHash: &badHash}))
	f.put(t, xdr.LedgerKey{Type: xdr.LedgerEntryTypeContractCode, ContractCode: &xdr.LedgerKeyContractCode{Hash: badHash}}, codeEntry(badHash, wasm))

	missing, _ := contractAddr(t, 4)

	rpcErr, rpcErrAddr := contractAddr(t, 5)
	k, _ := xdr.MarshalBase64(instanceKey(rpcErrAddr))
	f.raw[k] = `{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"boom"}}`

	huge, hugeAddr := contractAddr(t, 6)
	k, _ = xdr.MarshalBase64(instanceKey(hugeAddr))
	f.raw[k] = `{"pad":"` + strings.Repeat("x", maxResponseBytes) + `"}`

	return f, map[string]string{"oracle": oracle, "sac": sac, "tampered": tampered, "missing": missing, "rpc_error": rpcErr, "huge": huge}
}

func TestRPCSpec(t *testing.T) {
	f, c := world(t)
	srv := httptest.NewServer(f)
	defer srv.Close()
	r := RPC{URL: srv.URL}
	ctx := context.Background()

	s, err := r.Spec(ctx, c["oracle"])
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Function("set_price"); !ok {
		t.Fatal("set_price not in the fetched spec")
	}

	tests := map[string]struct {
		contract string
		want     string
		is       error
	}{
		"sac":       {c["sac"], "does not run wasm", ErrNotWasm},
		"tampered":  {c["tampered"], "does not hash to the instance's wasm hash", nil},
		"missing":   {c["missing"], "returned 0 entries", nil},
		"rpc_error": {c["rpc_error"], "rpc error: boom", nil},
		"huge":      {c["huge"], "rpc response over", nil},
		"not_c":     {"GAAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQDZ7H", "not a contract address", nil},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := r.Spec(ctx, tt.contract)
			if err == nil || !strings.Contains(err.Error(), tt.want) || (tt.is != nil && !errors.Is(err, tt.is)) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestRPCCancelled(t *testing.T) {
	f, c := world(t)
	srv := httptest.NewServer(f)
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (RPC{URL: srv.URL}).Spec(ctx, c["oracle"]); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func call(addr xdr.ScAddress, fn string, subs ...xdr.SorobanAuthorizedInvocation) xdr.SorobanAuthorizedInvocation {
	return xdr.SorobanAuthorizedInvocation{
		Function: xdr.SorobanAuthorizedFunction{
			Type:       xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn,
			ContractFn: &xdr.InvokeContractArgs{ContractAddress: addr, FunctionName: xdr.ScSymbol(fn)},
		},
		SubInvocations: subs,
	}
}

// TestFetchDegrades: one entry calling a readable contract, a SAC and an
// unreachable one yields the one spec and a reason for each other, and
// never an overall error.
func TestFetchDegrades(t *testing.T) {
	f, c := world(t)
	srv := httptest.NewServer(f)
	defer srv.Close()
	addr := func(s string) xdr.ScAddress {
		raw, err := strkey.Decode(strkey.VersionByteContract, s)
		if err != nil {
			t.Fatal(err)
		}
		id := xdr.ContractId(raw)
		return xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &id}
	}
	entry := xdr.SorobanAuthorizationEntry{RootInvocation: call(addr(c["oracle"]), "set_price",
		call(addr(c["sac"]), "transfer"), call(addr(c["missing"]), "f"), call(addr(c["oracle"]), "get_price"))}

	if got := Contracts(entry); strings.Join(got, ",") != strings.Join([]string{c["oracle"], c["sac"], c["missing"]}, ",") {
		t.Fatalf("Contracts = %v", got)
	}
	specs, errs := RPC{URL: srv.URL}.Fetch(context.Background(), entry)
	if len(specs) != 1 || specs[c["oracle"]].Len() != 10 {
		t.Fatalf("specs = %v", specs)
	}
	if len(errs) != 2 || !errors.Is(errs[c["sac"]], ErrNotWasm) || errs[c["missing"]] == nil {
		t.Fatalf("errs = %v", errs)
	}
}

func TestContractsBounded(t *testing.T) {
	var subs []xdr.SorobanAuthorizedInvocation
	for i := 0; i < 200; i++ {
		_, a := contractAddr(t, byte(i))
		subs = append(subs, call(a, "f"))
	}
	_, root := contractAddr(t, 255)
	got := Contracts(xdr.SorobanAuthorizationEntry{RootInvocation: call(root, "f", subs...)})
	if len(got) != maxContracts {
		t.Fatalf("%d contracts, want the bound %d", len(got), maxContracts)
	}
}
