package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

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
	{name: "asset", desc: "a candidate asset as CODE:ISSUER; repeat for several"},
	{name: "rpc", desc: "a Soroban RPC URL to fetch contract specs from (makes network requests)"},
	{name: "json", desc: "print the stable JSON rendering instead of text", isBool: true},
	{name: "strict", desc: "exit 3 unless the explanation is decoded", isBool: true},
}

// completionsFlags is the completion spec for the completions subcommand,
// checked against newCompletionsFlagSet the same way.
var completionsFlags = []flagSpec{
	{name: "shell", desc: "the shell to print completions for", values: shells},
}

// shells are the shells completions can be printed for.
var shells = []string{"bash", "zsh", "fish"}

// subcommands are offered as the first word. Explain is the default command
// and has no name of its own.
var subcommands = []struct{ name, desc string }{
	{"completions", "print a shell completion script"},
	{"help", "print usage"},
}

func newCompletionsFlagSet(stderr io.Writer) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("soroauth-explain completions", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, "usage: soroauth-explain completions --shell bash|zsh|fish\n") }
	shell := fs.String("shell", "", "the shell to print completions for")
	return fs, shell
}

func runCompletions(args []string, stdout, stderr io.Writer) int {
	fs, shell := newCompletionsFlagSet(stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "soroauth-explain completions: unexpected argument %q\n", fs.Arg(0))
		fs.Usage()
		return exitUsage
	}
	var script string
	switch *shell {
	case "bash":
		script = bashCompletion()
	case "zsh":
		script = zshCompletion()
	case "fish":
		script = fishCompletion()
	default:
		fmt.Fprintf(stderr, "soroauth-explain completions: --shell must be one of %s\n", strings.Join(shells, ", "))
		fs.Usage()
		return exitUsage
	}
	io.WriteString(stdout, script)
	return exitOK
}

func flagWords(specs []flagSpec) string {
	words := make([]string, len(specs))
	for i, f := range specs {
		words[i] = "--" + f.name
	}
	return strings.Join(words, " ")
}

func bashCompletion() string {
	var b strings.Builder
	b.WriteString("# bash completion for soroauth-explain\n")
	b.WriteString("_soroauth_explain() {\n")
	b.WriteString("  local cur=\"${COMP_WORDS[COMP_CWORD]}\" prev=\"${COMP_WORDS[COMP_CWORD-1]}\"\n")
	b.WriteString("  COMPREPLY=()\n")
	b.WriteString("  if [[ \"${COMP_WORDS[1]}\" == completions ]]; then\n")
	b.WriteString("    case \"$prev\" in\n")
	writeBashValueCases(&b, completionsFlags, "      ")
	b.WriteString("    esac\n")
	b.WriteString("    COMPREPLY=( $(compgen -W \"" + flagWords(completionsFlags) + "\" -- \"$cur\") )\n")
	b.WriteString("    return 0\n")
	b.WriteString("  fi\n")
	b.WriteString("  case \"$prev\" in\n")
	writeBashValueCases(&b, completionFlags, "    ")
	b.WriteString("  esac\n")
	b.WriteString("  local words=\"" + flagWords(completionFlags) + "\"\n")
	var subs []string
	for _, s := range subcommands {
		subs = append(subs, s.name)
	}
	b.WriteString("  if [[ $COMP_CWORD -eq 1 ]]; then words=\"$words " + strings.Join(subs, " ") + "\"; fi\n")
	b.WriteString("  COMPREPLY=( $(compgen -W \"$words\" -- \"$cur\") )\n")
	b.WriteString("}\n")
	b.WriteString("complete -F _soroauth_explain soroauth-explain\n")
	return b.String()
}

// writeBashValueCases emits a case arm for each flag that takes a value:
// its fixed choices, or nothing (so no flag names are offered as values).
func writeBashValueCases(b *strings.Builder, specs []flagSpec, indent string) {
	for _, f := range specs {
		if f.isBool {
			continue
		}
		b.WriteString(indent + "--" + f.name + ") COMPREPLY=( $(compgen -W \"" + strings.Join(f.values, " ") + "\" -- \"$cur\") ); return 0 ;;\n")
	}
}

// zshEscape escapes the characters _arguments treats specially in a
// description.
func zshEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`, `:`, `\:`, `'`, `'\''`).Replace(s)
}

func zshSpecs(specs []flagSpec) string {
	var b strings.Builder
	for _, f := range specs {
		b.WriteString(" \\\n    '--" + f.name + "[" + zshEscape(f.desc) + "]")
		if !f.isBool {
			b.WriteString(":" + f.name + ":")
			if len(f.values) > 0 {
				b.WriteString("(" + strings.Join(f.values, " ") + ")")
			}
		}
		b.WriteString("'")
	}
	return b.String()
}

func zshCompletion() string {
	var b strings.Builder
	b.WriteString("#compdef soroauth-explain\n")
	b.WriteString("# zsh completion for soroauth-explain\n")
	b.WriteString("_soroauth_explain() {\n")
	b.WriteString("  if (( CURRENT > 2 )) && [[ ${words[2]} == completions ]]; then\n")
	b.WriteString("    _arguments" + zshSpecs(completionsFlags) + "\n")
	b.WriteString("    return\n")
	b.WriteString("  fi\n")
	var subs []string
	for _, s := range subcommands {
		// Inside ((...)) a space separates entries, so a description's
		// spaces are escaped.
		subs = append(subs, s.name+"\\:"+strings.ReplaceAll(zshEscape(s.desc), " ", "\\ "))
	}
	b.WriteString("  _arguments" + zshSpecs(completionFlags) + " \\\n    '1::command:((" + strings.Join(subs, " ") + "))'\n")
	b.WriteString("}\n")
	b.WriteString("compdef _soroauth_explain soroauth-explain\n")
	return b.String()
}

func fishQuote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

func fishCompletion() string {
	var b strings.Builder
	b.WriteString("# fish completion for soroauth-explain\n")
	b.WriteString("complete -c soroauth-explain -f\n")
	for _, s := range subcommands {
		b.WriteString("complete -c soroauth-explain -n '__fish_use_subcommand' -a " + s.name + " -d " + fishQuote(s.desc) + "\n")
	}
	write := func(cond string, specs []flagSpec) {
		for _, f := range specs {
			line := "complete -c soroauth-explain -n " + fishQuote(cond) + " -l " + f.name
			if !f.isBool {
				if len(f.values) > 0 {
					line += " -x -a " + fishQuote(strings.Join(f.values, " "))
				} else {
					line += " -r"
				}
			}
			b.WriteString(line + " -d " + fishQuote(f.desc) + "\n")
		}
	}
	write("not __fish_seen_subcommand_from completions", completionFlags)
	write("__fish_seen_subcommand_from completions", completionsFlags)
	return b.String()
}
