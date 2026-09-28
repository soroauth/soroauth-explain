package interfaces

// SEP41 is the Soroban token interface, SEP-41 version 0.5.2
// (stellar/stellar-protocol ecosystem/sep-0041.md, section "Specification /
// Interface", read at commit 9cd703075d87a6ce293752b1532e7b68efe12ae1 on
// 2026-09-28). Each signature below is copied from that trait:
//
//	fn approve(env: Env, from: Address, spender: Address, amount: i128, live_until_ledger: u32);
//	fn transfer(env: Env, from: Address, to: MuxedAddress, amount: i128);
//	fn transfer_from(env: Env, spender: Address, from: Address, to: Address, amount: i128);
//	fn burn(env: Env, from: Address, amount: i128);
//	fn burn_from(env: Env, spender: Address, from: Address, amount: i128);
//
// Only the functions that change state are registered. allowance, balance,
// decimals, name and symbol are reads; an authorization entry naming one of
// them is unusual enough that this library prefers to show it as unknown
// rather than describe it.
//
// The summaries follow the SEP's own doc comments: approve replaces the
// current allowance rather than adding to it (SEP-41 v0.5.1 changelog), and
// transfer_from and burn_from consume the spender's allowance.
var SEP41 = []Signature{
	{
		Interface: "SEP-41", Kind: "token_approve", Function: "approve",
		Params: []Param{
			{"from", ArgAddress}, {"spender", ArgAddress}, {"amount", ArgAmount}, {"live_until_ledger", ArgLedger},
		},
		AssetSummary: "Allow {spender} to spend up to {amount} {asset} from {from} until ledger {live_until_ledger}, replacing any current allowance",
		Summary:      "Allow {spender} to spend up to {amount} units of the token at {contract} from {from} until ledger {live_until_ledger}, replacing any current allowance",
	},
	{
		Interface: "SEP-41", Kind: "token_transfer", Function: "transfer",
		Params: []Param{
			{"from", ArgAddress}, {"to", ArgMuxedAddress}, {"amount", ArgAmount},
		},
		AssetSummary: "Transfer {amount} {asset} from {from} to {to}",
		Summary:      "Transfer {amount} units of the token at {contract} from {from} to {to}",
	},
	{
		Interface: "SEP-41", Kind: "token_transfer_from", Function: "transfer_from",
		Params: []Param{
			{"spender", ArgAddress}, {"from", ArgAddress}, {"to", ArgAddress}, {"amount", ArgAmount},
		},
		AssetSummary: "Transfer {amount} {asset} from {from} to {to}, spending the allowance of {spender}",
		Summary:      "Transfer {amount} units of the token at {contract} from {from} to {to}, spending the allowance of {spender}",
	},
	{
		Interface: "SEP-41", Kind: "token_burn", Function: "burn",
		Params: []Param{
			{"from", ArgAddress}, {"amount", ArgAmount},
		},
		AssetSummary: "Burn {amount} {asset} from {from}",
		Summary:      "Burn {amount} units of the token at {contract} from {from}",
	},
	{
		Interface: "SEP-41", Kind: "token_burn_from", Function: "burn_from",
		Params: []Param{
			{"spender", ArgAddress}, {"from", ArgAddress}, {"amount", ArgAmount},
		},
		AssetSummary: "Burn {amount} {asset} from {from}, spending the allowance of {spender}",
		Summary:      "Burn {amount} units of the token at {contract} from {from}, spending the allowance of {spender}",
	},
}
