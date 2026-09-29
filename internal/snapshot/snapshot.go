// Package snapshot is the one place that turns a committed input entry into
// its committed renderings. The generator (cmd/gensnapshots) and the gate
// (snapshot_test.go) both call Render, so they cannot disagree about how a
// case is rendered; they can only disagree about what the code does, which is
// what the gate exists to catch.
package snapshot

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	explain "github.com/soroauth/soroauth-explain"
	"github.com/soroauth/soroauth-explain/render"
	"github.com/soroauth/soroauth-explain/spec"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// Formats are the rendering formats every case has a snapshot for, as file
// extensions.
var Formats = []string{"txt", "json"}

// Entry is one input case in testdata/entries.
type Entry struct {
	Name              string   `json:"name"`
	Source            string   `json:"source"`
	NetworkPassphrase string   `json:"network_passphrase"`
	Assets            []string `json:"assets,omitempty"` // candidates for explain.WithAssets, as CODE:ISSUER
	// Specs are contract spec sections (spec.Section), base64, keyed by
	// contract address, for explain.WithSpecs.
	Specs      map[string]string `json:"specs,omitempty"`
	SpecSource string            `json:"spec_source,omitempty"`
	EntryXDR   string            `json:"entry_xdr"`
}

// ParseSpecs decodes an entry's recorded spec sections.
func ParseSpecs(specs map[string]string) (map[string]spec.Spec, error) {
	out := make(map[string]spec.Spec, len(specs))
	for c, b64 := range specs {
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("spec for %s: %w", c, err)
		}
		s, err := spec.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("spec for %s: %w", c, err)
		}
		out[c] = s
	}
	return out, nil
}

// ParseAssets turns CODE:ISSUER strings into assets.
func ParseAssets(labels []string) ([]xdr.Asset, error) {
	out := make([]xdr.Asset, 0, len(labels))
	for _, l := range labels {
		code, issuer, ok := strings.Cut(l, ":")
		if !ok {
			return nil, fmt.Errorf("asset %q is not CODE:ISSUER", l)
		}
		a, err := xdr.NewCreditAsset(code, issuer)
		if err != nil {
			return nil, fmt.Errorf("asset %q: %w", l, err)
		}
		out = append(out, a)
	}
	return out, nil
}

// LoadEntries reads every *.json in dir, sorted by case name.
func LoadEntries(dir string) ([]Entry, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(files))
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var e Entry
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if e.Name+".json" != filepath.Base(f) {
			return nil, fmt.Errorf("%s: name %q does not match the file name", f, e.Name)
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

// Render produces a case's rendering in every format, keyed by extension.
//
// An entry that Explain refuses is still a case: its snapshot records the
// refusal, so a change in what is refused is as visible as a change in what
// is said. Only an entry that does not decode is an error here, because that
// is a broken fixture rather than a behaviour.
func Render(e Entry) (map[string][]byte, error) {
	var entry xdr.SorobanAuthorizationEntry
	if err := xdr.SafeUnmarshalBase64(e.EntryXDR, &entry); err != nil {
		return nil, fmt.Errorf("%s: decode entry: %w", e.Name, err)
	}
	assets, err := ParseAssets(e.Assets)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", e.Name, err)
	}
	specs, err := ParseSpecs(e.Specs)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", e.Name, err)
	}
	exp, err := explain.Explain(entry, explain.WithNetwork(e.NetworkPassphrase), explain.WithAssets(assets...), explain.WithSpecs(specs))
	if err != nil {
		refusal, jerr := json.MarshalIndent(map[string]string{"error": err.Error()}, "", "  ")
		if jerr != nil {
			return nil, jerr
		}
		return map[string][]byte{
			"txt":  []byte("error: " + err.Error() + "\n"),
			"json": append(refusal, '\n'),
		}, nil
	}
	js, err := render.JSON(exp)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", e.Name, err)
	}
	return map[string][]byte{
		"txt":  []byte(render.Text(exp)),
		"json": js,
	}, nil
}

// Path is where a case's snapshot for a format lives.
func Path(dir, name, format string) string {
	return filepath.Join(dir, name+"."+format)
}

// Read returns a committed snapshot. A missing snapshot is
// explain.ErrNoSnapshot: an input nobody has reviewed the rendering of.
func Read(dir, name, format string) ([]byte, error) {
	b, err := os.ReadFile(Path(dir, name, format))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("explain: read snapshot %s.%s: %w", name, format, explain.ErrNoSnapshot)
	}
	return b, err
}
