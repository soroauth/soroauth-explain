package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/soroauth/soroauth-explain/internal/snapshot"
	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/xdr"
)

const (
	entriesDir   = "../../testdata/entries"
	snapshotsDir = "../../testdata/snapshots"
)

func runCLI(t testing.TB, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, strings.NewReader(stdin), &out, &errOut)
	return out.String(), errOut.String(), code
}

func loadEntry(t testing.TB, name string) snapshot.Entry {
	t.Helper()
	entries, err := snapshot.LoadEntries(entriesDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("no entry %s", name)
	return snapshot.Entry{}
}

// TestCLIMatchesSnapshots runs every committed input through the CLI, in
// both formats, and requires the committed snapshot on stdout. A refused
// entry must exit 1 with nothing on stdout.
func TestCLIMatchesSnapshots(t *testing.T) {
	entries, err := snapshot.LoadEntries(entriesDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no entries")
	}
	for _, e := range entries {
		t.Run(e.Name, func(t *testing.T) {
			txt, err := snapshot.Read(snapshotsDir, e.Name, "txt")
			if err != nil {
				t.Fatal(err)
			}
			js, err := snapshot.Read(snapshotsDir, e.Name, "json")
			if err != nil {
				t.Fatal(err)
			}
			refused := bytes.HasPrefix(txt, []byte("error: "))
			for _, tc := range []struct {
				args []string
				want []byte
			}{
				{[]string{"--entry", e.EntryXDR, "--network", e.NetworkPassphrase}, txt},
				{[]string{"--entry", e.EntryXDR, "--network", e.NetworkPassphrase, "--json"}, js},
			} {
				out, errOut, code := runCLI(t, "", tc.args...)
				if refused {
					if code != exitError || out != "" || !strings.Contains(errOut, strings.TrimPrefix(strings.TrimSpace(string(txt)), "error: ")) {
						t.Fatalf("refusal: code %d, stdout %q, stderr %q", code, out, errOut)
					}
					continue
				}
				if code != exitOK || out != string(tc.want) || errOut != "" {
					t.Fatalf("code %d, stderr %q\n--- stdout ---\n%s\n--- snapshot ---\n%s", code, errOut, out, tc.want)
				}
			}
		})
	}
}

func TestCLIStdin(t *testing.T) {
	e := loadEntry(t, "built_native_sac_transfer")
	want, err := snapshot.Read(snapshotsDir, e.Name, "txt")
	if err != nil {
		t.Fatal(err)
	}
	for name, stdin := range map[string]string{
		"trailing_newline": e.EntryXDR + "\n",
		"crlf":             e.EntryXDR + "\r\n",
		"surrounding":      "  \t" + e.EntryXDR + " \n\n",
		"bare":             e.EntryXDR,
	} {
		t.Run(name, func(t *testing.T) {
			out, errOut, code := runCLI(t, stdin, "--entry", "-", "--network", "testnet")
			if code != exitOK || out != string(want) {
				t.Fatalf("code %d, stderr %q, stdout:\n%s", code, errOut, out)
			}
		})
	}
	t.Run("flag_value_trimmed", func(t *testing.T) {
		out, _, code := runCLI(t, "", "--entry", e.EntryXDR+"\n", "--network", "testnet")
		if code != exitOK || out != string(want) {
			t.Fatalf("code %d", code)
		}
	})
	t.Run("empty", func(t *testing.T) {
		out, errOut, code := runCLI(t, "\n", "--entry", "-")
		if code != exitError || out != "" || !strings.Contains(errOut, "input is empty") {
			t.Fatalf("code %d, stdout %q, stderr %q", code, out, errOut)
		}
	})
	t.Run("over_limit", func(t *testing.T) {
		big := strings.Repeat("A", base64.StdEncoding.EncodedLen(maxDecodeBytes)+stdinSlack+1)
		out, errOut, code := runCLI(t, big, "--entry", "-")
		if code != exitError || out != "" || !strings.Contains(errOut, errDecodeLimit.Error()) {
			t.Fatalf("code %d, stdout %q, stderr %q", code, out, errOut)
		}
	})
}

// TestCLIRealPipe builds the binary and feeds it through a shell pipe, with
// the trailing newline a real producer prints.
func TestCLIRealPipe(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "soroauth-explain")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	e := loadEntry(t, "built_native_sac_transfer")
	want, err := snapshot.Read(snapshotsDir, e.Name, "json")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", `printf '%s\n' "$ENTRY" | "$BIN" --entry - --network testnet --json`)
	cmd.Env = append(os.Environ(), "ENTRY="+e.EntryXDR, "BIN="+bin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("pipe: %v\nstderr: %s", err, stderr.String())
	}
	if !bytes.Equal(stdout.Bytes(), want) {
		t.Fatalf("piped output differs from the snapshot:\n%s", stdout.String())
	}

	// A failing pipe still leaves stdout clean for `--json | jq`.
	cmd = exec.Command("sh", "-c", `printf 'not-base64\n' | "$BIN" --entry - --json`)
	cmd.Env = append(os.Environ(), "BIN="+bin)
	stdout.Reset()
	stderr.Reset()
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != exitError || stdout.Len() != 0 || stderr.Len() == 0 {
		t.Fatalf("failing pipe: err %v, stdout %q, stderr %q", err, stdout.String(), stderr.String())
	}
}

func TestCLIStrict(t *testing.T) {
	decoded := loadEntry(t, "built_native_sac_transfer")
	partial := loadEntry(t, "built_impostor_transfer")

	out, _, code := runCLI(t, "", "--entry", decoded.EntryXDR, "--network", "testnet", "--strict")
	if code != exitOK || !strings.Contains(out, "[decoded]") {
		t.Fatalf("decoded under --strict: code %d", code)
	}
	out, errOut, code := runCLI(t, "", "--entry", partial.EntryXDR, "--network", "testnet", "--strict")
	if code != exitNotDecoded || !strings.Contains(errOut, "partial, not decoded") {
		t.Fatalf("partial under --strict: code %d, stderr %q", code, errOut)
	}
	if !strings.Contains(out, "Not determined:") {
		t.Fatal("--strict failure must still print the rendering")
	}
	// Without --network nothing is labelled, so the SAC transfer is partial
	// and --strict fails: the flag a policy engine relies on cannot pass by
	// omission.
	if _, _, code := runCLI(t, "", "--entry", decoded.EntryXDR, "--strict"); code != exitNotDecoded {
		t.Fatalf("no network under --strict: code %d", code)
	}
}

func TestCLINetworkNames(t *testing.T) {
	for name, want := range map[string]string{
		"testnet":  network.TestNetworkPassphrase,
		"public":   network.PublicNetworkPassphrase,
		"":         "",
		"My Net 1": "My Net 1",
	} {
		if got := networkPassphrase(name); got != want {
			t.Errorf("networkPassphrase(%q) = %q, want %q", name, got, want)
		}
	}
	e := loadEntry(t, "built_native_sac_transfer")
	for flagValue, wantLabel := range map[string]bool{"testnet": true, network.TestNetworkPassphrase: true, "public": false} {
		out, _, _ := runCLI(t, "", "--entry", e.EntryXDR, "--network", flagValue)
		if got := strings.Contains(out, "asset    = native"); got != wantLabel {
			t.Errorf("--network %q: labelled %v, want %v", flagValue, got, wantLabel)
		}
	}
}

func TestCLIUsage(t *testing.T) {
	e := loadEntry(t, "built_unknown_function")
	tests := []struct {
		name string
		args []string
		code int
	}{
		{"no_entry", nil, exitUsage},
		{"unknown_flag", []string{"--entry", e.EntryXDR, "--bogus"}, exitUsage},
		{"stray_argument", []string{"--entry", e.EntryXDR, "extra"}, exitUsage},
		{"unknown_subcommand", []string{"frobnicate"}, exitUsage},
		{"bad_base64", []string{"--entry", "!!!"}, exitError},
		{"bad_xdr", []string{"--entry", "AAAA"}, exitError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, errOut, code := runCLI(t, "", tt.args...)
			if code != tt.code || out != "" || errOut == "" {
				t.Fatalf("code %d (want %d), stdout %q, stderr %q", code, tt.code, out, errOut)
			}
		})
	}
	for _, h := range []string{"help", "-h", "--help"} {
		out, _, code := runCLI(t, "", h)
		if code != exitOK || !strings.Contains(out, "usage:") {
			t.Fatalf("%s: code %d", h, code)
		}
	}
}

func nestedVec(levels int) xdr.ScVal {
	u := xdr.Uint32(0)
	v := xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &u}
	for i := 0; i < levels; i++ {
		vec := xdr.ScVec{v}
		p := &vec
		v = xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &p}
	}
	return v
}

func entryWithArg(t testing.TB, arg xdr.ScVal) string {
	t.Helper()
	id := xdr.ContractId{1}
	entry := xdr.SorobanAuthorizationEntry{
		Credentials: xdr.SorobanCredentials{Type: xdr.SorobanCredentialsTypeSorobanCredentialsSourceAccount},
		RootInvocation: xdr.SorobanAuthorizedInvocation{Function: xdr.SorobanAuthorizedFunction{
			Type: xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn,
			ContractFn: &xdr.InvokeContractArgs{
				ContractAddress: xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &id},
				FunctionName:    "f",
				Args:            []xdr.ScVal{arg},
			},
		}},
	}
	b64, err := xdr.MarshalBase64(entry)
	if err != nil {
		t.Fatal(err)
	}
	return b64
}

func TestDecodeDepthLimit(t *testing.T) {
	// Find the deepest nesting that decodes, and check the next level is
	// refused as a limit rather than as malformed input.
	deepest := -1
	for levels := 0; levels <= maxDecodeDepth; levels++ {
		if _, err := decodeEntry(entryWithArg(t, nestedVec(levels))); err != nil {
			if !errors.Is(err, errDecodeLimit) {
				t.Fatalf("%d levels: err = %v, want errDecodeLimit", levels, err)
			}
			break
		}
		deepest = levels
	}
	if deepest < 0 || deepest >= maxDecodeDepth {
		t.Fatalf("deepest decodable nesting = %d; the %d-level limit never bit", deepest, maxDecodeDepth)
	}
	t.Logf("deepest decodable vector nesting under MaxDepth %d: %d levels", maxDecodeDepth, deepest)

	// The same entry decodes under the SDK default, so it is this tool's
	// limit that refuses it, not malformed bytes.
	var entry xdr.SorobanAuthorizationEntry
	if err := xdr.SafeUnmarshalBase64(entryWithArg(t, nestedVec(deepest+1)), &entry); err != nil {
		t.Fatalf("SDK default refused it too: %v", err)
	}

	_, errOut, code := runCLI(t, "", "--entry", entryWithArg(t, nestedVec(deepest+1)))
	if code != exitError || !strings.Contains(errOut, "nesting exceeds the 64-level limit") {
		t.Fatalf("CLI: code %d, stderr %q", code, errOut)
	}
}

// TestDecodeLengthLimitComesFirst: an over-long input is refused by length
// before base64 is touched. The input is not valid base64, so any other
// ordering would report a base64 error instead.
func TestDecodeLengthLimitComesFirst(t *testing.T) {
	max := base64.StdEncoding.EncodedLen(maxDecodeBytes)
	_, err := decodeEntry(strings.Repeat("!", max+1))
	if !errors.Is(err, errDecodeLimit) {
		t.Fatalf("over limit: err = %v, want errDecodeLimit", err)
	}
	_, err = decodeEntry(strings.Repeat("!", max))
	if err == nil || errors.Is(err, errDecodeLimit) {
		t.Fatalf("at limit: err = %v, want a base64 error, not the length limit", err)
	}
	// A valid encoding of exactly maxDecodeBytes passes the length check
	// and fails only as XDR.
	_, err = decodeEntry(base64.StdEncoding.EncodeToString(make([]byte, maxDecodeBytes)))
	if errors.Is(err, errDecodeLimit) {
		t.Fatalf("exactly %d bytes: refused by length", maxDecodeBytes)
	}
}

// TestCompletionSpecMatchesFlags: every registered flag is in the completion
// spec, and the spec has nothing that is not registered.
func TestCompletionSpecMatchesFlags(t *testing.T) {
	fs, _ := newExplainFlagSet(&bytes.Buffer{})
	var registered []string
	fs.VisitAll(func(f *flag.Flag) { registered = append(registered, f.Name) })
	var spec []string
	for _, f := range completionFlags {
		spec = append(spec, f.name)
		fl := fs.Lookup(f.name)
		if fl == nil {
			continue
		}
		_, isBool := fl.Value.(interface{ IsBoolFlag() bool })
		if isBool != f.isBool {
			t.Errorf("--%s: spec isBool %v, flag isBool %v", f.name, f.isBool, isBool)
		}
		if fl.Usage != f.desc {
			t.Errorf("--%s: spec description %q differs from flag usage %q", f.name, f.desc, fl.Usage)
		}
	}
	sort.Strings(registered)
	sort.Strings(spec)
	if strings.Join(registered, ",") != strings.Join(spec, ",") {
		t.Fatalf("registered flags %v, completion spec %v", registered, spec)
	}
}
