package explain

import "strings"

// ActionKind names what kind of invocation an Action describes. It is a
// string so the JSON wire format stays readable and stable.
type ActionKind string

const (
	// ActionInvokeContract is a call to a contract function
	// (SOROBAN_AUTHORIZED_FUNCTION_TYPE_CONTRACT_FN). Unless a registry entry
	// interprets it, its arguments are shown as raw values and it is opaque.
	ActionInvokeContract ActionKind = "invoke_contract"

	// ActionCreateContract is contract creation through the host
	// (SOROBAN_AUTHORIZED_FUNCTION_TYPE_CREATE_CONTRACT_HOST_FN, and the V2
	// form with constructor arguments). Its shape is defined by the protocol
	// rather than by a contract, so it can be read from the bytes alone.
	ActionCreateContract ActionKind = "create_contract"
)

// Action is one node of an authorized invocation tree, explained.
//
// Summary is one line for a person; Fields carries the same information
// structured, for a wallet that renders its own interface. Every value that
// appears in Summary also appears in some Field, so the two cannot disagree.
type Action struct {
	Kind       ActionKind `json:"kind"`
	Contract   string     `json:"contract"`
	Function   string     `json:"function"`
	Confidence Confidence `json:"confidence"`
	Summary    string     `json:"summary"`
	Fields     []Field    `json:"fields,omitempty"`
	Sub        []Action   `json:"sub,omitempty"`
}

// Field is one named value of an Action.
//
// Value is what a person is shown. Whenever Value is an interpretation rather
// than the value itself, Raw carries the undecorated value from the bytes, so
// a reviewer can always see what was actually there.
type Field struct {
	Name       string     `json:"name"`
	Value      string     `json:"value"`
	Raw        string     `json:"raw,omitempty"`
	Confidence Confidence `json:"confidence"`
}

// summarize fills a template's {name} placeholders from fields. Building every
// Summary this way is what keeps it from stating a value no Field carries.
func summarize(template string, fields []Field) string {
	pairs := make([]string, 0, 2*len(fields))
	for _, f := range fields {
		pairs = append(pairs, "{"+f.Name+"}", f.Value)
	}
	return strings.NewReplacer(pairs...).Replace(template)
}
