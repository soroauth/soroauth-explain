package main

import (
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	explain "github.com/soroauth/soroauth-explain"
	"github.com/soroauth/soroauth-explain/render"
	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// The decode limits are this tool's own guarantee about hostile input, not
// borrowed ones: an entry arrives from whoever sent it, and the bound must not
// move because a dependency's constant did.
//
// They match soroauth-go's (main, decode.go:35 MaxDecodeDepth = 64 and
// decode.go:39 MaxDecodeInputBytes = 1 << 20, read at 0a45f4c on 2026-09-28).
// If soroauth-go is ever re-pinned to a release with DecodeAuthorizationEntry,
// add a test asserting the two agree; do not swap one for the other.
const (
	// maxDecodeDepth bounds XDR nesting during decoding. The SDK's default
	// is 1500 (go-xdr xdr3/decode.go:36), far past anything a valid entry
	// needs, and Explain refuses below this anyway (DefaultMaxDepth 32).
	maxDecodeDepth = 64

	// maxDecodeBytes bounds the decoded size of an entry: 1 MiB.
	maxDecodeBytes = 1 << 20

	// stdinSlack is how much surrounding whitespace --entry - tolerates on
	// top of the longest valid base64 input.
	stdinSlack = 4096
)

// errDecodeLimit marks an input refused by a decode limit, as distinct from
// input that is malformed.
var errDecodeLimit = errors.New("input exceeds a decode limit")

// decodeEntry decodes an untrusted base64 authorization entry within this
// tool's limits.
//
// The length is checked first, before any base64 or XDR work, because
// SafeUnmarshalBase64WithOptions overwrites MaxInputLen with the decoded
// length of whatever it is handed and so can never refuse long input itself
// (go-stellar-sdk v0.7.3 xdr/main.go:68-69; the same ordering argument is
// made in soroauth-go main decode.go:66-68).
func decodeEntry(encoded string) (xdr.SorobanAuthorizationEntry, error) {
	if encoded == "" {
		return xdr.SorobanAuthorizationEntry{}, errors.New("decode entry: input is empty")
	}
	if len(encoded) > base64.StdEncoding.EncodedLen(maxDecodeBytes) {
		return xdr.SorobanAuthorizationEntry{}, fmt.Errorf("decode entry: %d base64 bytes exceeds the %d-byte decoded limit: %w",
			len(encoded), maxDecodeBytes, errDecodeLimit)
	}
	var entry xdr.SorobanAuthorizationEntry
	err := xdr.SafeUnmarshalBase64WithOptions(encoded, &entry, xdr.DecodeOptions{MaxDepth: maxDecodeDepth})
	if errors.Is(err, xdr.ErrMaxDecodingDepthReached) {
		return xdr.SorobanAuthorizationEntry{}, fmt.Errorf("decode entry: nesting exceeds the %d-level limit: %w", maxDecodeDepth, errDecodeLimit)
	}
	if err != nil {
		return xdr.SorobanAuthorizationEntry{}, fmt.Errorf("decode entry: %w", err)
	}
	return entry, nil
}

// networkPassphrase resolves --network. The names are the SDK's constants
// (go-stellar-sdk v0.7.3 network/main.go:16,18); anything else is taken as a
// passphrase verbatim. Empty means no network, and so no asset labels.
func networkPassphrase(name string) string {
	switch name {
	case "testnet":
		return network.TestNetworkPassphrase
	case "public":
		return network.PublicNetworkPassphrase
	}
	return name
}

type explainFlags struct {
	entry   string
	network string
	assets  assetList
	json    bool
	strict  bool
}

// assetList is the repeatable --asset flag. Each value must parse as
// CODE:ISSUER; anything else is rejected when the flag is parsed, so a
// mistyped candidate is a usage error rather than one silently dropped.
type assetList struct {
	labels []string
	assets []xdr.Asset
}

func (l *assetList) String() string { return strings.Join(l.labels, ",") }

func (l *assetList) Set(v string) error {
	code, issuer, ok := strings.Cut(v, ":")
	if !ok {
		if v == "native" {
			return errors.New("native is always a candidate and needs no --asset")
		}
		return fmt.Errorf("%q is not CODE:ISSUER", v)
	}
	a, err := xdr.NewCreditAsset(code, issuer)
	if err != nil {
		return fmt.Errorf("%q is not CODE:ISSUER: %v", v, err)
	}
	l.labels = append(l.labels, v)
	l.assets = append(l.assets, a)
	return nil
}

// newExplainFlagSet registers the explain flags. The completions spec is
// checked against exactly this set.
func newExplainFlagSet(stderr io.Writer) (*flag.FlagSet, *explainFlags) {
	f := &explainFlags{}
	fs := flag.NewFlagSet("soroauth-explain", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usageText) }
	fs.StringVar(&f.entry, "entry", "", "the entry as base64 XDR, or - to read it from stdin")
	fs.StringVar(&f.network, "network", "", "testnet, public, or a network passphrase")
	fs.Var(&f.assets, "asset", "a candidate asset as CODE:ISSUER; repeat for several")
	fs.BoolVar(&f.json, "json", false, "print the stable JSON rendering instead of text")
	fs.BoolVar(&f.strict, "strict", false, "exit 3 unless the explanation is decoded")
	return fs, f
}

func runExplain(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs, f := newExplainFlagSet(stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "soroauth-explain: unexpected argument %q\n", fs.Arg(0))
		fs.Usage()
		return exitUsage
	}
	if f.entry == "" {
		fmt.Fprintln(stderr, "soroauth-explain: --entry is required")
		fs.Usage()
		return exitUsage
	}

	encoded := f.entry
	if encoded == "-" {
		limit := int64(base64.StdEncoding.EncodedLen(maxDecodeBytes) + stdinSlack)
		raw, err := io.ReadAll(io.LimitReader(stdin, limit+1))
		if err != nil {
			fmt.Fprintf(stderr, "soroauth-explain: read stdin: %v\n", err)
			return exitError
		}
		if int64(len(raw)) > limit {
			fmt.Fprintf(stderr, "soroauth-explain: stdin exceeds %d bytes: %v\n", limit, errDecodeLimit)
			return exitError
		}
		encoded = string(raw)
	}
	// Tools that print an entry end it with a newline, and the XDR decoder
	// rejects one, so surrounding whitespace is trimmed on both paths.
	encoded = strings.TrimSpace(encoded)

	entry, err := decodeEntry(encoded)
	if err != nil {
		fmt.Fprintf(stderr, "soroauth-explain: %v\n", err)
		return exitError
	}

	var opts []explain.Option
	if pp := networkPassphrase(f.network); pp != "" {
		opts = append(opts, explain.WithNetwork(pp))
	}
	if len(f.assets.assets) > 0 {
		opts = append(opts, explain.WithAssets(f.assets.assets...))
	}
	exp, err := explain.Explain(entry, opts...)
	if err != nil {
		fmt.Fprintf(stderr, "soroauth-explain: %v\n", err)
		return exitError
	}

	if f.json {
		out, err := render.JSON(exp)
		if err != nil {
			fmt.Fprintf(stderr, "soroauth-explain: %v\n", err)
			return exitError
		}
		stdout.Write(out)
	} else {
		io.WriteString(stdout, render.Text(exp))
	}

	if f.strict && exp.Confidence != explain.ConfidenceDecoded {
		fmt.Fprintf(stderr, "soroauth-explain: --strict: explanation is %s, not decoded\n", exp.Confidence)
		return exitNotDecoded
	}
	return exitOK
}
