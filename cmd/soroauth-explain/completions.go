package main

// flagSpec describes one flag for shell completion.
type flagSpec struct {
	name   string
	desc   string
	values []string // fixed choices to offer, if any
	isBool bool
}

// completionFlags is the completion spec for the explain flags. Every flag
// registered by newExplainFlagSet must appear here and nothing else may;
// TestCompletionSpecMatchesFlags enforces both directions.
var completionFlags = []flagSpec{
	{name: "entry", desc: "the entry as base64 XDR, or - to read it from stdin"},
	{name: "network", desc: "testnet, public, or a network passphrase", values: []string{"testnet", "public"}},
	{name: "json", desc: "print the stable JSON rendering instead of text", isBool: true},
	{name: "strict", desc: "exit 3 unless the explanation is decoded", isBool: true},
}
