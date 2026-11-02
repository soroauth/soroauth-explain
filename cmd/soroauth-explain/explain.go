package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	explain "github.com/soroauth/soroauth-explain"
	"github.com/soroauth/soroauth-explain/render"
	"github.com/soroauth/soroauth-explain/spec"
	soroauth "github.com/soroauth/soroauth-go"
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

// rpcTimeout bounds all of --rpc's requests for one input together.
const rpcTimeout = 30 * time.Second

// errDecodeLimit marks an input refused by a decode limit, as distinct from
// input that is malformed.
var errDecodeLimit = errors.New("input exceeds a decode limit")

// decodeEntry decodes an untrusted base64 authorization entry within this
// tool's limits.
func decodeEntry(encoded string) (xdr.SorobanAuthorizationEntry, error) {
	var entry xdr.SorobanAuthorizationEntry
	if err := decodeLimited("entry", encoded, &entry); err != nil {
		return xdr.SorobanAuthorizationEntry{}, err
	}
	return entry, nil
}

// decodeEnvelope decodes an untrusted base64 transaction envelope within the
// same limits as an entry. An envelope's entries are nested inside it, so a
// limit that bounds an entry alone bounds them all together here.
func decodeEnvelope(encoded string) (xdr.TransactionEnvelope, error) {
	var env xdr.TransactionEnvelope
	if err := decodeLimited("envelope", encoded, &env); err != nil {
		return xdr.TransactionEnvelope{}, err
	}
	return env, nil
}

// decodeLimited decodes untrusted base64 XDR into v within this tool's
// limits.
//
// The length is checked first, before any base64 or XDR work, because
// SafeUnmarshalBase64WithOptions overwrites MaxInputLen with the decoded
// length of whatever it is handed and so can never refuse long input itself
// (go-stellar-sdk v0.7.3 xdr/main.go:68-69; the same ordering argument is
// made in soroauth-go main decode.go:66-68).
func decodeLimited(what, encoded string, v any) error {
	if encoded == "" {
		return fmt.Errorf("decode %s: input is empty", what)
	}
	if len(encoded) > base64.StdEncoding.EncodedLen(maxDecodeBytes) {
		return fmt.Errorf("decode %s: %d base64 bytes exceeds the %d-byte decoded limit: %w",
			what, len(encoded), maxDecodeBytes, errDecodeLimit)
	}
	err := xdr.SafeUnmarshalBase64WithOptions(encoded, v, xdr.DecodeOptions{MaxDepth: maxDecodeDepth})
	if errors.Is(err, xdr.ErrMaxDecodingDepthReached) {
		return fmt.Errorf("decode %s: nesting exceeds the %d-level limit: %w", what, maxDecodeDepth, errDecodeLimit)
	}
	if err != nil {
		return fmt.Errorf("decode %s: %w", what, err)
	}
	return nil
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
	entry    string
	envelope string
	network  string
	rpc      string
	assets   assetList
	json     bool
	strict   bool
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
	fs.StringVar(&f.envelope, "envelope", "", "a transaction envelope as base64 XDR, or - to read it from stdin")
	fs.StringVar(&f.network, "network", "", "testnet, public, or a network passphrase")
	fs.Var(&f.assets, "asset", "a candidate asset as CODE:ISSUER; repeat for several")
	fs.StringVar(&f.rpc, "rpc", "", "a Soroban RPC URL to fetch contract specs from (makes network requests)")
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
	if f.rpc != "" {
		if u, err := url.Parse(f.rpc); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			fmt.Fprintf(stderr, "soroauth-explain: --rpc %q is not an http or https URL\n", f.rpc)
			fs.Usage()
			return exitUsage
		}
	}
	if (f.entry == "") == (f.envelope == "") {
		fmt.Fprintln(stderr, "soroauth-explain: exactly one of --entry and --envelope is required")
		fs.Usage()
		return exitUsage
	}

	encoded := f.entry + f.envelope
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
	// Tools that print XDR end it with a newline, and the XDR decoder
	// rejects one, so surrounding whitespace is trimmed on both paths.
	encoded = strings.TrimSpace(encoded)

	var opts []explain.Option
	if pp := networkPassphrase(f.network); pp != "" {
		opts = append(opts, explain.WithNetwork(pp))
	}
	if len(f.assets.assets) > 0 {
		opts = append(opts, explain.WithAssets(f.assets.assets...))
	}

	var (
		text       string
		jsonOut    func() ([]byte, error)
		confidence explain.Confidence
	)
	if f.envelope != "" {
		env, err := decodeEnvelope(encoded)
		if err != nil {
			fmt.Fprintf(stderr, "soroauth-explain: %v\n", err)
			return exitError
		}
		if f.rpc != "" {
			opts = append(opts, fetchSpecs(f.rpc, envelopeContracts(env), stderr))
		}
		exp, err := explain.ExplainEnvelope(env, opts...)
		if err != nil {
			fmt.Fprintf(stderr, "soroauth-explain: %v\n", err)
			return exitError
		}
		text, confidence = render.EnvelopeText(exp), exp.Confidence
		jsonOut = func() ([]byte, error) { return render.EnvelopeJSON(exp) }
	} else {
		entry, err := decodeEntry(encoded)
		if err != nil {
			fmt.Fprintf(stderr, "soroauth-explain: %v\n", err)
			return exitError
		}
		if f.rpc != "" {
			opts = append(opts, fetchSpecs(f.rpc, spec.Contracts(entry), stderr))
		}
		exp, err := explain.Explain(entry, opts...)
		if err != nil {
			fmt.Fprintf(stderr, "soroauth-explain: %v\n", err)
			return exitError
		}
		text, confidence = render.Text(exp), exp.Confidence
		jsonOut = func() ([]byte, error) { return render.JSON(exp) }
	}

	if f.json {
		out, err := jsonOut()
		if err != nil {
			fmt.Fprintf(stderr, "soroauth-explain: %v\n", err)
			return exitError
		}
		stdout.Write(out)
	} else {
		io.WriteString(stdout, text)
	}

	if f.strict && confidence != explain.ConfidenceDecoded {
		fmt.Fprintf(stderr, "soroauth-explain: --strict: explanation is %s, not decoded\n", confidence)
		return exitNotDecoded
	}
	return exitOK
}

// fetchSpecs is the only network access in this tool, and runs only when
// --rpc is given. A fetch that fails leaves that contract unnamed; it never
// fails the command, and it never changes what is decoded.
func fetchSpecs(rpcURL string, contracts []string, stderr io.Writer) explain.Option {
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	r := spec.RPC{URL: rpcURL}
	specs := map[string]spec.Spec{}
	for _, c := range contracts {
		s, err := r.Spec(ctx, c)
		if err != nil {
			if !errors.Is(err, spec.ErrNotWasm) {
				fmt.Fprintf(stderr, "soroauth-explain: --rpc: no spec for %s: %v\n", c, err)
			}
			continue
		}
		specs[c] = s
	}
	return explain.WithSpecs(specs)
}

// maxEnvelopeContracts bounds how many contracts --rpc looks up for one
// envelope, so a transaction naming many contracts cannot turn into
// unbounded requests. spec.Contracts bounds each entry the same way.
const maxEnvelopeContracts = 64

// envelopeContracts lists the distinct contracts an envelope invokes
// directly and through its entries, in order, up to maxEnvelopeContracts.
func envelopeContracts(env xdr.TransactionEnvelope) []string {
	var ops []xdr.Operation
	switch env.Type {
	case xdr.EnvelopeTypeEnvelopeTypeTxV0:
		if env.V0 != nil {
			ops = env.V0.Tx.Operations
		}
	case xdr.EnvelopeTypeEnvelopeTypeTx:
		if env.V1 != nil {
			ops = env.V1.Tx.Operations
		}
	case xdr.EnvelopeTypeEnvelopeTypeTxFeeBump:
		if env.FeeBump != nil && env.FeeBump.Tx.InnerTx.V1 != nil {
			ops = env.FeeBump.Tx.InnerTx.V1.Tx.Operations
		}
	}
	var out []string
	seen := map[string]bool{}
	add := func(c string) {
		if !seen[c] && len(out) < maxEnvelopeContracts {
			seen[c] = true
			out = append(out, c)
		}
	}
	for _, op := range ops {
		ihf, ok := op.Body.GetInvokeHostFunctionOp()
		if !ok {
			continue
		}
		if args, ok := ihf.HostFunction.GetInvokeContract(); ok {
			if c, err := soroauth.FormatAddress(args.ContractAddress); err == nil {
				add(c)
			}
		}
		for _, entry := range ihf.Auth {
			for _, c := range spec.Contracts(entry) {
				add(c)
			}
		}
	}
	return out
}
