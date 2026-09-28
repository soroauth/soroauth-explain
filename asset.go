package explain

import (
	"fmt"

	soroauth "github.com/soroauth/soroauth-go"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// SACContractID returns the Stellar Asset Contract ID for an asset on a network.
//
// The ID is a deterministic function of the asset and the network passphrase:
// the SHA-256 of a HashIdPreimage of type ENVELOPE_TYPE_CONTRACT_ID carrying
// the network ID and a CONTRACT_ID_PREIMAGE_FROM_ASSET preimage. This wraps
// the SDK's own implementation, xdr.Asset.ContractID in go-stellar-sdk
// v0.7.3 xdr/asset.go:467, rather than re-deriving it. The derivation is
// checked against Stellar Asset Contract instances fetched from testnet and
// public network RPC and recorded in testdata/sac.
//
// It returns ErrUnknownNetwork for an empty passphrase: there is no network
// to derive for, and so nothing to compare against.
func SACContractID(asset xdr.Asset, networkPassphrase string) (string, error) {
	if networkPassphrase == "" {
		return "", fmt.Errorf("explain: sac contract id: %w", ErrUnknownNetwork)
	}
	if err := checkAssetArm(asset); err != nil {
		return "", fmt.Errorf("explain: sac contract id: %w", err)
	}
	id, err := asset.ContractID(networkPassphrase)
	if err != nil {
		return "", fmt.Errorf("explain: sac contract id: %w", err)
	}
	contractID := xdr.ContractId(id)
	address, err := soroauth.FormatAddress(xdr.ScAddress{
		Type:       xdr.ScAddressTypeScAddressTypeContract,
		ContractId: &contractID,
	})
	if err != nil {
		return "", fmt.Errorf("explain: sac contract id: %w", err)
	}
	return address, nil
}

// checkAssetArm rejects an asset whose discriminant names an arm that is nil.
// The SDK's encoder dereferences the arm without checking and panics
// (go-stellar-sdk v0.7.3 xdr/xdr_generated.go:2488, Asset.EncodeTo), so this
// must run before xdr.Asset.ContractID.
func checkAssetArm(a xdr.Asset) error {
	switch a.Type {
	case xdr.AssetTypeAssetTypeNative:
		return nil
	case xdr.AssetTypeAssetTypeCreditAlphanum4:
		if a.AlphaNum4 == nil {
			return emptyArm("asset alphanum4")
		}
		return nil
	case xdr.AssetTypeAssetTypeCreditAlphanum12:
		if a.AlphaNum12 == nil {
			return emptyArm("asset alphanum12")
		}
		return nil
	}
	return fmt.Errorf("unknown asset type %d", int32(a.Type))
}

// AssetLabel returns the asset label for a contract address, and true, only
// when the address is the derived SAC ID for one of the candidate assets on
// that network.
//
// This is the impostor guard. The only way a contract earns a label is by its
// ID being the derivation of an asset and the network passphrase. Nothing the
// contract reports about itself (its name, its symbol, its decimals), no name
// lookup, and no match on function signatures is consulted, because every one
// of those can be forged by a contract that is not the asset.
//
// The label is the SDK's canonical asset form (xdr.Asset.StringCanonical,
// go-stellar-sdk v0.7.3 xdr/asset.go:261): "native" for the native asset and
// CODE:ISSUER for an issued asset. The issuer is always included. An asset
// code alone does not identify an asset: anyone can issue "USDC", and its
// Stellar Asset Contract derives correctly for its own issuer. On the public
// network on 2026-09-28, USDC:GA5Z…KZVN and USDC:GAHK…S5HV both had deployed
// Stellar Asset Contracts reporting symbol "USDC" (testdata/sac).
//
// An empty passphrase, a candidate that cannot be derived, or a code that is
// not plain ASCII letters and digits yields no label.
func AssetLabel(contract string, networkPassphrase string, candidates []xdr.Asset) (string, bool) {
	if networkPassphrase == "" {
		return "", false
	}
	for i := range candidates {
		id, err := SACContractID(candidates[i], networkPassphrase)
		if err != nil {
			continue
		}
		if id != contract {
			continue
		}
		return canonicalAssetLabel(candidates[i])
	}
	return "", false
}

// canonicalAssetLabel mirrors xdr.Asset.StringCanonical but returns false
// instead of panicking on a malformed asset, and refuses any code that is not
// plain ASCII letters and digits so that a label cannot carry control
// characters or look-alike text into a rendering.
func canonicalAssetLabel(a xdr.Asset) (string, bool) {
	if checkAssetArm(a) != nil {
		return "", false
	}
	var typ, code, issuer string
	if err := a.Extract(&typ, &code, &issuer); err != nil {
		return "", false
	}
	if a.Type == xdr.AssetTypeAssetTypeNative {
		return typ, true
	}
	if code == "" {
		return "", false
	}
	for i := 0; i < len(code); i++ {
		c := code[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return "", false
		}
	}
	return code + ":" + issuer, true
}
