// Command soroauth-explain explains what a Soroban authorization entry
// authorizes, and says what it could not determine.
//
//	soroauth-explain --entry <base64|-> [--network testnet|public|<passphrase>]
//	                 [--asset CODE:ISSUER] [--rpc <url>] [--json] [--strict]
//	soroauth-explain completions --shell bash|zsh|fish
//
// Results go to stdout and diagnostics to stderr, so stdout only ever carries
// a result: a rendering, or nothing.
//
// Exit status: 0 success; 1 the entry could not be read, decoded or
// explained; 2 usage error; 3 --strict was given and the explanation is not
// decoded (the rendering is still printed).
package main

import (
	"fmt"
	"io"
	"os"
)

const (
	exitOK         = 0
	exitError      = 1
	exitUsage      = 2
	exitNotDecoded = 3
)

const usageText = `usage:
  soroauth-explain --entry <base64|-> [--network testnet|public|<passphrase>]
                   [--asset CODE:ISSUER] [--rpc <url>] [--json] [--strict]
  soroauth-explain completions --shell bash|zsh|fish

Explains what a Soroban authorization entry authorizes. Every node is marked
decoded, partial or opaque; anything not decoded is listed under
"Not determined".

flags:
  --entry     the entry as base64 XDR, or - to read it from stdin
  --network   testnet, public, or a network passphrase; without it no
              contract is labelled with an asset
  --asset     a candidate asset as CODE:ISSUER, repeatable. A contract is
              labelled with it only if it is that asset's derived Stellar
              Asset Contract on --network. native needs no flag.
  --rpc       a Soroban RPC URL. When given, this command makes network
              requests (getLedgerEntries) to that URL to fetch the published
              spec of each contract the entry calls, and uses it to name
              arguments. Named calls stay partial; --strict still fails on
              them. If a fetch fails, the rest is explained as without --rpc.
              Without --rpc, nothing is sent anywhere.
  --json      print the stable JSON rendering instead of text
  --strict    exit 3 unless the explanation is decoded

commands:
  completions print a shell completion script (--shell bash|zsh|fish)
  help        print this usage
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run dispatches a command line. Every subcommand is reached from here, and
// the tests call run rather than a subcommand directly, so a subcommand that
// is not dispatched cannot pass its tests.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "completions":
			return runCompletions(args[1:], stdout, stderr)
		case "help", "-h", "--help":
			fmt.Fprint(stdout, usageText)
			return exitOK
		}
	}
	return runExplain(args, stdin, stdout, stderr)
}
