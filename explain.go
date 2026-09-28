package explain

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/soroauth/soroauth-explain/interfaces"
	soroauth "github.com/soroauth/soroauth-go"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// registry is the set of known functions Explain interprets.
var registry = interfaces.Default()

// sacDecimals is the number of decimal places every Stellar Asset Contract
// uses. It is a host constant, not something the contract reports:
// rs-soroban-env soroban-env-host/src/builtin_contracts/stellar_asset_contract/
// metadata.rs:22 `pub const DECIMAL: u32 = 7;`, returned by decimals() at
// contract.rs:376-379 (read at 169890d50e27ef948bef66401147aba814d9b04e on
// 2026-09-28). A contract earns it only by its ID deriving as a SAC.
const sacDecimals = 7

// CredentialTypeDelegate is the CredentialType of an Explanation that
// describes a delegate node rather than an entry's own credentials.
const CredentialTypeDelegate = "delegate"

// Explanation is an account of what one authorization entry authorizes.
//
// Structure (credential arm, addresses, nonce, expiration, which nodes are
// signed) comes from soroauth.Inspect. Meaning is added per Action, and
// Confidence is the floor across every action and every delegate. Whenever
// Confidence is not ConfidenceDecoded, Unexplained says, in plain sentences,
// what could not be determined; it is the text a wallet shows in a warning.
type Explanation struct {
	Subject          string        `json:"subject,omitempty"`
	CredentialType   string        `json:"credential_type"`
	AddressBound     bool          `json:"address_bound"`
	Confidence       Confidence    `json:"confidence"`
	Nonce            int64         `json:"nonce,omitempty"`
	ValidUntilLedger uint32        `json:"valid_until_ledger,omitempty"`
	Signed           bool          `json:"signed"`
	Actions          []Action      `json:"actions"`
	Delegates        []Explanation `json:"delegates,omitempty"`
	Unexplained      []string      `json:"unexplained,omitempty"`
}

// Option configures Explain.
type Option func(*options)

type options struct {
	network  string
	maxDepth int
	maxNodes int
}

// WithNetwork sets the network passphrase. It is required for any asset
// labelling: a Stellar Asset Contract ID is derived from the asset and the
// passphrase, so without it no contract is ever labelled.
func WithNetwork(passphrase string) Option {
	return func(o *options) { o.network = passphrase }
}

// WithMaxDepth overrides DefaultMaxDepth. A value below 1 is an error.
func WithMaxDepth(n int) Option {
	return func(o *options) { o.maxDepth = n }
}

// WithMaxNodes overrides DefaultMaxNodes. A value below 1 is an error.
func WithMaxNodes(n int) Option {
	return func(o *options) { o.maxNodes = n }
}

// Explain describes what an authorization entry authorizes.
//
// It never modifies entry. It does not reach the network, and so takes no
// context. Structure comes from soroauth.Inspect and is not re-derived here,
// so the two libraries cannot disagree about the same entry; an Inspect
// error is returned wrapped.
//
// A source-account entry carries no address, nonce or signature of its own:
// the transaction's source account authenticates it through the envelope
// signature. That changes who authorizes, not what: its invocation tree is
// explained exactly like any other entry's, and its confidence is the floor
// across those actions. Leaving the tree out would show a reader an entry
// with nothing in it, while it authorizes calls they were never shown.
func Explain(entry xdr.SorobanAuthorizationEntry, opts ...Option) (Explanation, error) {
	o := options{maxDepth: DefaultMaxDepth, maxNodes: DefaultMaxNodes}
	for _, opt := range opts {
		opt(&o)
	}
	if o.maxDepth < 1 {
		return Explanation{}, fmt.Errorf("explain: explain entry: max depth %d is below 1", o.maxDepth)
	}
	if o.maxNodes < 1 {
		return Explanation{}, fmt.Errorf("explain: explain entry: max nodes %d is below 1", o.maxNodes)
	}

	info, err := soroauth.Inspect(entry)
	if err != nil {
		if errors.Is(err, soroauth.ErrUnsupportedCredentials) {
			return Explanation{}, fmt.Errorf("explain: explain entry: %w: %w", ErrUnsupportedCredentials, err)
		}
		return Explanation{}, fmt.Errorf("explain: explain entry: %w", err)
	}

	exp := Explanation{
		Subject:          info.Address,
		CredentialType:   info.CredentialType,
		AddressBound:     info.AddressBound,
		Confidence:       ConfidenceDecoded,
		Nonce:            info.Nonce,
		ValidUntilLedger: info.ValidUntilLedger,
		Signed:           info.TopLevelSigned,
		Actions:          []Action{},
	}
	w := newWalker(o.maxDepth, o.maxNodes)
	root, notes, err := w.explainInvocation(entry.RootInvocation, 1, o)
	if err != nil {
		return Explanation{}, fmt.Errorf("explain: explain entry: %w", err)
	}
	exp.Actions = []Action{root}
	exp.Confidence = Floor(exp.Confidence, root.Confidence)
	exp.Unexplained = notes

	delegates, err := w.explainDelegates(info.Delegates, exp.Actions, exp.Confidence, notes, 1)
	if err != nil {
		return Explanation{}, fmt.Errorf("explain: explain entry: %w", err)
	}
	exp.Delegates = delegates
	for i := range delegates {
		exp.Confidence = Floor(exp.Confidence, delegates[i].Confidence)
	}
	return exp, nil
}

// explainDelegates describes each delegate node. Under CAP-71-01 the account
// and every delegate at every depth sign the same payload, bound to the
// top-level address (soroauth-go v0.1.0 delegates.go:15-16, preimage.go:70-71),
// so each delegate authorizes exactly the entry's actions and carries their
// confidence and caveats. A delegate shown on its own must never look more
// certain than what it signs.
func (w *walker) explainDelegates(nodes []soroauth.NodeInfo, actions []Action, conf Confidence, notes []string, depth int) ([]Explanation, error) {
	if len(nodes) == 0 {
		return nil, nil
	}
	out := make([]Explanation, 0, len(nodes))
	for i := range nodes {
		if err := w.visit(depth); err != nil {
			return nil, err
		}
		nested, err := w.explainDelegates(nodes[i].Nested, actions, conf, notes, depth+1)
		if err != nil {
			return nil, err
		}
		d := Explanation{
			Subject:        nodes[i].Address,
			CredentialType: CredentialTypeDelegate,
			AddressBound:   true,
			Confidence:     conf,
			Signed:         nodes[i].Signed,
			Actions:        actions,
			Delegates:      nested,
			Unexplained:    notes,
		}
		for j := range nested {
			d.Confidence = Floor(d.Confidence, nested[j].Confidence)
		}
		out = append(out, d)
	}
	return out, nil
}

// explainInvocation explains one invocation and its sub-invocations, in
// order. It returns the notes for everything not decoded, in tree pre-order.
func (w *walker) explainInvocation(inv xdr.SorobanAuthorizedInvocation, depth int, o options) (Action, []string, error) {
	if err := w.visit(depth); err != nil {
		return Action{}, nil, err
	}

	var (
		action Action
		notes  []string
		err    error
	)
	fn := inv.Function
	switch fn.Type {
	case xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn:
		if fn.ContractFn == nil {
			return Action{}, nil, emptyArm("contract_fn")
		}
		action, notes, err = w.explainContractFn(*fn.ContractFn, depth, o)
	case xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeCreateContractHostFn:
		if fn.CreateContractHostFn == nil {
			return Action{}, nil, emptyArm("create_contract_host_fn")
		}
		action, notes, err = w.explainCreateContract(fn.CreateContractHostFn.ContractIdPreimage,
			fn.CreateContractHostFn.Executable, nil, depth)
	case xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeCreateContractV2HostFn:
		if fn.CreateContractV2HostFn == nil {
			return Action{}, nil, emptyArm("create_contract_v2_host_fn")
		}
		args := fn.CreateContractV2HostFn.ConstructorArgs
		if args == nil {
			args = []xdr.ScVal{}
		}
		action, notes, err = w.explainCreateContract(fn.CreateContractV2HostFn.ContractIdPreimage,
			fn.CreateContractV2HostFn.Executable, args, depth)
	default:
		return Action{}, nil, fmt.Errorf("unknown authorized function type %d", int32(fn.Type))
	}
	if err != nil {
		return Action{}, nil, err
	}

	for i := range inv.SubInvocations {
		sub, subNotes, err := w.explainInvocation(inv.SubInvocations[i], depth+1, o)
		if err != nil {
			return Action{}, nil, err
		}
		action.Sub = append(action.Sub, sub)
		action.Confidence = Floor(action.Confidence, sub.Confidence)
		notes = append(notes, subNotes...)
	}
	return action, notes, nil
}

// argumentsShownRaw says how many arguments are shown uninterpreted, with the
// verb agreeing with the count.
func argumentsShownRaw(n int) string {
	switch n {
	case 0:
		return "the call passes no arguments"
	case 1:
		return "its 1 argument is shown as a raw value"
	}
	return "its " + strconv.Itoa(n) + " arguments are shown as raw values"
}

func pluralArguments(n int) string {
	if n == 1 {
		return "argument"
	}
	return "arguments"
}

// explainContractFn explains a contract call. A call matching a registered
// signature is interpreted by explainKnown; anything else is opaque, with its
// arguments shown as raw values.
func (w *walker) explainContractFn(fn xdr.InvokeContractArgs, depth int, o options) (Action, []string, error) {
	contract, err := soroauth.FormatAddress(fn.ContractAddress)
	if err != nil {
		return Action{}, nil, err
	}

	name := string(fn.FunctionName)
	fnField := Field{Name: "function", Value: name, Confidence: ConfidenceDecoded}
	var notes []string
	if !isPlainSymbol(name) {
		fnField.Value = strconv.Quote(name)
		fnField.Confidence = ConfidenceOpaque
		notes = append(notes, fmt.Sprintf("The function name on %s contains characters outside A-Z, a-z, 0-9 and underscore, and is shown quoted.", contract))
	}

	// The only label a contract can earn is the asset whose Stellar Asset
	// Contract ID it is, on the caller's network. Without WithNetwork there
	// is nothing to derive, and so no label.
	label, isSAC := AssetLabel(contract, o.network, []xdr.Asset{xdr.MustNewNativeAsset()})

	if sig, ok := registry.Lookup(name, fn.Args); ok && fnField.Confidence == ConfidenceDecoded {
		return w.explainKnown(sig, contract, label, isSAC, fn.Args, depth, o)
	}

	fields := []Field{
		{Name: "contract", Value: contract, Confidence: ConfidenceDecoded},
		fnField,
		{Name: "arguments", Value: strconv.Itoa(len(fn.Args)), Confidence: ConfidenceDecoded},
	}
	template := "Call {function} on {contract} with {arguments} " + pluralArguments(len(fn.Args))

	if isSAC {
		fields = append(fields, Field{Name: "asset", Value: label, Confidence: ConfidenceDecoded})
		template = "Call {function} on {contract}, the Stellar Asset Contract for {asset}, with {arguments} " + pluralArguments(len(fn.Args))
	}

	for i := range fn.Args {
		v, err := w.renderScVal(fn.Args[i], depth+1)
		if err != nil {
			return Action{}, nil, err
		}
		fields = append(fields, Field{Name: "arg[" + strconv.Itoa(i) + "]", Value: v.text, Confidence: v.conf})
		for _, n := range v.notes {
			notes = append(notes, fmt.Sprintf("Argument %d of %s on %s: %s", i, fnField.Value, contract, n))
		}
	}

	notes = append(notes, fmt.Sprintf("The function %s on %s is not interpreted by this library; %s.",
		fnField.Value, contract, argumentsShownRaw(len(fn.Args))))

	return Action{
		Kind:       ActionInvokeContract,
		Contract:   contract,
		Function:   fnField.Value,
		Confidence: ConfidenceOpaque,
		Summary:    summarize(template, fields),
		Fields:     fields,
	}, notes, nil
}

// explainCreateContract explains a host contract creation. The preimage and
// executable are protocol-defined and read from the bytes. Constructor
// arguments (the V2 form, args non-nil) are shown as raw values: their meaning
// belongs to the contract being created, so any present make the action
// partial.
func (w *walker) explainCreateContract(pre xdr.ContractIdPreimage, exe xdr.ContractExecutable, args []xdr.ScVal, depth int) (Action, []string, error) {
	var (
		fields   []Field
		notes    []string
		template string
	)
	conf := ConfidenceDecoded

	switch pre.Type {
	case xdr.ContractIdPreimageTypeContractIdPreimageFromAddress:
		if pre.FromAddress == nil {
			return Action{}, nil, emptyArm("contract id preimage from address")
		}
		deployer, err := soroauth.FormatAddress(pre.FromAddress.Address)
		if err != nil {
			return Action{}, nil, err
		}
		ex, err := renderExecutable(exe)
		if err != nil {
			return Action{}, nil, err
		}
		fields = []Field{
			{Name: "executable", Value: ex.text, Confidence: ex.conf},
			{Name: "deployer", Value: deployer, Confidence: ConfidenceDecoded},
			{Name: "salt", Value: hex.EncodeToString(pre.FromAddress.Salt[:]), Confidence: ConfidenceDecoded},
		}
		conf = Floor(conf, ex.conf)
		notes = append(notes, ex.notes...)
		template = "Create a contract running {executable}, deployed by {deployer} with salt {salt}"

	case xdr.ContractIdPreimageTypeContractIdPreimageFromAsset:
		if pre.FromAsset == nil {
			return Action{}, nil, emptyArm("contract id preimage from asset")
		}
		label, ok := canonicalAssetLabel(*pre.FromAsset)
		f := Field{Name: "asset", Value: label, Confidence: ConfidenceDecoded}
		if !ok {
			f.Value = "unrenderable asset"
			f.Confidence = ConfidenceOpaque
			notes = append(notes, "The asset whose Stellar Asset Contract is being deployed cannot be rendered.")
		}
		ex, err := renderExecutable(exe)
		if err != nil {
			return Action{}, nil, err
		}
		fields = []Field{f, {Name: "executable", Value: ex.text, Confidence: ex.conf}}
		conf = Floor(Floor(conf, f.Confidence), ex.conf)
		notes = append(notes, ex.notes...)
		// Only the stellar_asset executable makes this the asset's own
		// contract; any other pairing is described literally.
		template = "Create a contract from asset {asset}, running {executable}"
		if exe.Type == xdr.ContractExecutableTypeContractExecutableStellarAsset {
			template = "Deploy the Stellar Asset Contract for {asset} ({executable})"
		}

	default:
		return Action{}, nil, fmt.Errorf("unknown contract id preimage type %d", int32(pre.Type))
	}

	if args != nil {
		fields = append(fields, Field{Name: "arguments", Value: strconv.Itoa(len(args)), Confidence: ConfidenceDecoded})
		template += ", with {arguments} constructor " + pluralArguments(len(args))
		for i := range args {
			v, err := w.renderScVal(args[i], depth+1)
			if err != nil {
				return Action{}, nil, err
			}
			fields = append(fields, Field{Name: "arg[" + strconv.Itoa(i) + "]", Value: v.text, Confidence: v.conf})
			conf = Floor(conf, v.conf)
			for _, n := range v.notes {
				notes = append(notes, fmt.Sprintf("Constructor argument %d: %s", i, n))
			}
		}
		if len(args) > 0 {
			conf = Floor(conf, ConfidencePartial)
			notes = append(notes, fmt.Sprintf("Constructor arguments: %s; their meaning depends on the contract being created.",
				argumentsShownRaw(len(args))))
		}
	}

	return Action{
		Kind:       ActionCreateContract,
		Confidence: conf,
		Summary:    summarize(template, fields),
		Fields:     fields,
	}, notes, nil
}

// explainKnown interprets a call that matches a registered signature.
//
// The match says what the arguments are; the contract's identity decides how
// much of that may be stated. On a Stellar Asset Contract derived on the
// caller's network, the asset is named and amounts are scaled by the host's
// fixed decimal count, so every element is derived: decoded. On any other
// contract the asset and decimals are unknown and a matching signature does
// not show the contract behaves as the interface describes, so amounts stay
// raw integers and the action is at most partial.
func (w *walker) explainKnown(sig interfaces.Signature, contract, label string, isSAC bool, args []xdr.ScVal, depth int, o options) (Action, []string, error) {
	fields := []Field{
		{Name: "contract", Value: contract, Confidence: ConfidenceDecoded},
		{Name: "function", Value: sig.Function, Confidence: ConfidenceDecoded},
	}
	template := sig.Summary
	conf := ConfidencePartial
	if isSAC {
		fields = append(fields, Field{Name: "asset", Value: label, Confidence: ConfidenceDecoded})
		template = sig.AssetSummary
		conf = ConfidenceDecoded
	}

	var notes []string
	for i, p := range sig.Params {
		var f Field
		switch p.Type {
		case interfaces.ArgAmount:
			raw := i128String(*args[i].I128)
			if isSAC {
				f = Field{Name: p.Name, Value: scaleDecimal(raw, sacDecimals), Raw: raw, Confidence: ConfidenceDecoded}
			} else {
				f = Field{Name: p.Name, Value: raw, Confidence: ConfidencePartial}
			}
		case interfaces.ArgLedger:
			f = Field{Name: p.Name, Value: strconv.FormatUint(uint64(*args[i].U32), 10), Confidence: ConfidenceDecoded}
		default:
			v, err := w.renderScVal(args[i], depth+1)
			if err != nil {
				return Action{}, nil, err
			}
			f = Field{Name: p.Name, Value: v.text, Confidence: v.conf}
			for _, n := range v.notes {
				notes = append(notes, fmt.Sprintf("The %s of %s on %s: %s", p.Name, sig.Function, contract, n))
			}
		}
		// Amounts and ledgers are scalars; charge them to the budget like
		// any other rendered value.
		if p.Type == interfaces.ArgAmount || p.Type == interfaces.ArgLedger {
			if err := w.visit(depth + 1); err != nil {
				return Action{}, nil, err
			}
		}
		conf = Floor(conf, f.Confidence)
		fields = append(fields, f)
	}

	if !isSAC {
		if o.network == "" {
			notes = append(notes, fmt.Sprintf("No network passphrase was given, so the token at %s is not identified; its asset and decimal places are unknown, and amounts are shown as raw integers in its smallest unit.", contract))
		} else {
			notes = append(notes, fmt.Sprintf("The token at %s is not identified on this network, so its asset and decimal places are unknown; amounts are shown as raw integers in its smallest unit.", contract))
		}
		notes = append(notes, fmt.Sprintf("The call matches the %s %s signature, but a matching signature does not show that the contract at %s behaves as %s describes.",
			sig.Interface, sig.Function, contract, sig.Interface))
	}

	return Action{
		Kind:       ActionKind(sig.Kind),
		Contract:   contract,
		Function:   sig.Function,
		Confidence: conf,
		Summary:    summarize(template, fields),
		Fields:     fields,
	}, notes, nil
}

// scaleDecimal places a decimal point in an integer string so that it has
// exactly places digits after the point: scaleDecimal("1000000000", 7) is
// "100.0000000". It works on the digits, never on a float.
func scaleDecimal(integer string, places int) string {
	n, ok := new(big.Int).SetString(integer, 10)
	if !ok {
		return integer
	}
	sign := ""
	if n.Sign() < 0 {
		sign = "-"
		n.Neg(n)
	}
	digits := n.String()
	if len(digits) <= places {
		digits = strings.Repeat("0", places-len(digits)+1) + digits
	}
	return sign + digits[:len(digits)-places] + "." + digits[len(digits)-places:]
}
