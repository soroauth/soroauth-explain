package spec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	soroauth "github.com/soroauth/soroauth-go"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// ErrNotWasm is returned for a contract that runs no wasm, such as a Stellar
// Asset Contract, and so has no spec to read.
var ErrNotWasm = errors.New("contract does not run wasm")

// maxResponseBytes bounds one RPC response. The largest is a ContractCode
// entry: MaxWasmBytes of code, base64-encoded (4/3), inside a small JSON
// envelope.
const maxResponseBytes = MaxWasmBytes*4/3 + 64<<10

// maxContracts bounds how many distinct contracts Fetch looks up for one
// entry, and maxWalk how many invocations it visits finding them. An entry
// that exceeds either still gets the specs found so far.
const (
	maxContracts = 64
	maxWalk      = 1024
)

// RPC fetches contract specs from a Soroban RPC endpoint.
//
// It is the only part of this module that reaches the network. It never
// signs, submits or simulates anything: it reads two ledger entries per
// contract with getLedgerEntries.
type RPC struct {
	// URL is the Soroban RPC endpoint.
	URL string
	// Client is the HTTP client to use; nil means http.DefaultClient.
	Client *http.Client
}

// Wasm fetches the code a contract runs. It reads the contract's instance
// for its wasm hash, then the ContractCode entry for that hash, and refuses
// code whose SHA-256 is not that hash.
func (r RPC) Wasm(ctx context.Context, contract string) ([]byte, error) {
	addr, err := soroauth.ParseAddress(contract)
	if err != nil {
		return nil, fmt.Errorf("spec: fetch %s: %w", contract, err)
	}
	if addr.Type != xdr.ScAddressTypeScAddressTypeContract {
		return nil, fmt.Errorf("spec: fetch %s: not a contract address", contract)
	}
	inst, err := r.entry(ctx, xdr.LedgerKey{Type: xdr.LedgerEntryTypeContractData, ContractData: &xdr.LedgerKeyContractData{
		Contract:   addr,
		Key:        xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance},
		Durability: xdr.ContractDataDurabilityPersistent,
	}})
	if err != nil {
		return nil, fmt.Errorf("spec: fetch %s instance: %w", contract, err)
	}
	cd, ok := inst.GetContractData()
	if !ok {
		return nil, fmt.Errorf("spec: fetch %s: instance entry is not contract data", contract)
	}
	instance, ok := cd.Val.GetInstance()
	if !ok {
		return nil, fmt.Errorf("spec: fetch %s: instance entry holds no instance", contract)
	}
	if instance.Executable.Type != xdr.ContractExecutableTypeContractExecutableWasm || instance.Executable.WasmHash == nil {
		return nil, fmt.Errorf("spec: fetch %s: %w", contract, ErrNotWasm)
	}
	hash := *instance.Executable.WasmHash
	codeEntry, err := r.entry(ctx, xdr.LedgerKey{Type: xdr.LedgerEntryTypeContractCode, ContractCode: &xdr.LedgerKeyContractCode{Hash: hash}})
	if err != nil {
		return nil, fmt.Errorf("spec: fetch %s code: %w", contract, err)
	}
	code, ok := codeEntry.GetContractCode()
	if !ok {
		return nil, fmt.Errorf("spec: fetch %s: code entry is not contract code", contract)
	}
	if sha256.Sum256(code.Code) != hash {
		return nil, fmt.Errorf("spec: fetch %s: code does not hash to the instance's wasm hash", contract)
	}
	return code.Code, nil
}

// Spec fetches and parses a contract's spec.
func (r RPC) Spec(ctx context.Context, contract string) (Spec, error) {
	wasm, err := r.Wasm(ctx, contract)
	if err != nil {
		return Spec{}, err
	}
	s, err := FromWasm(wasm)
	if err != nil {
		return Spec{}, fmt.Errorf("spec: %s: %w", contract, err)
	}
	return s, nil
}

// Fetch fetches the spec of every contract an entry calls, keyed by contract
// address, for explain.WithSpecs.
//
// It never fails as a whole. A contract whose spec cannot be fetched or read
// is left out of the specs and its reason is in the errors, so an
// explanation made with what was found degrades to what it would say with no
// specs at all.
func (r RPC) Fetch(ctx context.Context, entry xdr.SorobanAuthorizationEntry) (map[string]Spec, map[string]error) {
	specs := map[string]Spec{}
	errs := map[string]error{}
	for _, c := range Contracts(entry) {
		s, err := r.Spec(ctx, c)
		if err != nil {
			errs[c] = err
			continue
		}
		specs[c] = s
	}
	return specs, errs
}

// Contracts lists the distinct contracts an entry's invocation tree calls,
// in tree order, up to a fixed bound.
func Contracts(entry xdr.SorobanAuthorizationEntry) []string {
	var out []string
	seen := map[string]bool{}
	visited := 0
	var walk func(inv xdr.SorobanAuthorizedInvocation)
	walk = func(inv xdr.SorobanAuthorizedInvocation) {
		if visited >= maxWalk || len(out) >= maxContracts {
			return
		}
		visited++
		if fn, ok := inv.Function.GetContractFn(); ok {
			if c, err := soroauth.FormatAddress(fn.ContractAddress); err == nil && !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
		for _, sub := range inv.SubInvocations {
			walk(sub)
		}
	}
	walk(entry.RootInvocation)
	return out
}

// entry reads one ledger entry with getLedgerEntries.
func (r RPC) entry(ctx context.Context, key xdr.LedgerKey) (xdr.LedgerEntryData, error) {
	k, err := xdr.MarshalBase64(key)
	if err != nil {
		return xdr.LedgerEntryData{}, err
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "getLedgerEntries",
		"params": map[string]any{"keys": []string{k}},
	})
	if err != nil {
		return xdr.LedgerEntryData{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.URL, bytes.NewReader(body))
	if err != nil {
		return xdr.LedgerEntryData{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return xdr.LedgerEntryData{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return xdr.LedgerEntryData{}, fmt.Errorf("rpc status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return xdr.LedgerEntryData{}, err
	}
	if len(raw) > maxResponseBytes {
		return xdr.LedgerEntryData{}, fmt.Errorf("rpc response over %d bytes", maxResponseBytes)
	}
	var env struct {
		Result struct {
			Entries []struct {
				XDR string `json:"xdr"`
			} `json:"entries"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return xdr.LedgerEntryData{}, fmt.Errorf("rpc response: %w", err)
	}
	if env.Error != nil {
		return xdr.LedgerEntryData{}, fmt.Errorf("rpc error: %s", env.Error.Message)
	}
	if len(env.Result.Entries) != 1 {
		return xdr.LedgerEntryData{}, fmt.Errorf("rpc returned %d entries, want 1", len(env.Result.Entries))
	}
	var data xdr.LedgerEntryData
	opts := xdr.DecodeOptions{MaxDepth: maxSpecDepth}
	if err := xdr.SafeUnmarshalBase64WithOptions(env.Result.Entries[0].XDR, &data, opts); err != nil {
		return xdr.LedgerEntryData{}, fmt.Errorf("rpc entry: %w", err)
	}
	return data, nil
}
