package main

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	// fang renders with lipgloss v2; the v1 module (charmbracelet/lipgloss) is
	// used separately by internal/ui for command output.
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/fang"

	"github.com/Mozilla-Ocho/tabstack-cli/cmd"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/ui"
)

// Tabstack brand palette. Every hex comes from internal/ui so the fang theme
// here and the output styles there share one source of truth and cannot drift;
// the help-theme-only variants live alongside the rest as ui.HexPinkDeep/HexRose.
var (
	brandPurple      = lipgloss.Color(ui.HexPurple)
	brandPurpleLight = lipgloss.Color(ui.HexPurpleLight)
	brandPink        = lipgloss.Color(ui.HexPink)
	brandPinkDeep    = lipgloss.Color(ui.HexPinkDeep)
	brandInk         = lipgloss.Color(ui.HexInk)
	brandPaper       = lipgloss.Color(ui.HexPaper)
	brandGray        = lipgloss.Color(ui.HexGray)
	brandRed         = lipgloss.Color(ui.HexRose)
	brandWhite       = lipgloss.Color(ui.HexWhite)
)

// brandScheme themes fang's help, usage, and error output with the Tabstack
// brand colors. The c func picks the light- or dark-terminal variant.
func brandScheme(c lipgloss.LightDarkFunc) fang.ColorScheme {
	purple := c(brandPurple, brandPurpleLight)
	pink := c(brandPinkDeep, brandPink)
	body := c(brandInk, brandPaper)
	return fang.ColorScheme{
		Base:           body,
		Title:          purple,
		Description:    body,
		Codeblock:      c(lipgloss.Color("#f5f5f5"), lipgloss.Color("#1c1c1c")),
		Program:        purple,
		Command:        pink,
		DimmedArgument: brandGray,
		Comment:        brandGray,
		Flag:           purple,
		FlagDefault:    brandGray,
		QuotedString:   pink,
		Argument:       body,
		Help:           brandGray,
		Dash:           brandGray,
		ErrorHeader:    [2]color.Color{brandWhite, brandRed},
		ErrorDetails:   body,
	}
}

// exitErr is duplicated minimally here as an interface check so main does not
// need to import internals just for the type. Any error carrying a Code method
// sets the process exit code; everything else falls back to 1.
type coded interface {
	error
	Code() int
}

// errorHandler renders command failures. Cancellation is special-cased: the
// user pressed Ctrl-C, so a red ERROR box announcing what they just asked for
// is theatre. Everything else goes through fang's default rendering.
func errorHandler(w io.Writer, styles fang.Styles, err error) {
	if errors.Is(err, cmd.ErrInterrupted) {
		_, _ = fmt.Fprintln(w, "cancelled")
		return
	}
	fang.DefaultErrorHandler(w, styles, err)
}

func main() {
	root := cmd.NewRootCmd()

	// One signal handler for the whole tree. Every command threads this
	// context down to its request, so Ctrl-C cancels the call in flight and
	// the server is told, rather than the process being killed mid-request.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// fang runs the command tree with styled help, errors, and version output.
	// It prints any error itself (styled, to stderr) and returns it, so we keep
	// owning the exit-code mapping that makes this CLI scriptable.
	if err := fang.Execute(
		ctx,
		root,
		fang.WithVersion(cmd.Version()),
		fang.WithColorSchemeFunc(brandScheme),
		fang.WithErrorHandler(errorHandler),
	); err != nil {
		if c, ok := errors.AsType[coded](err); ok {
			os.Exit(c.Code())
		}
		if isCobraUsageError(err) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

// isCobraUsageError detects errors that Cobra emits for wrong argument counts,
// unknown commands, and unknown flags, all user mistakes that should exit 2.
//
// This relies on Cobra's error message prefixes (stable across v1.x, tested
// against v1.10.2). A more robust fix would replace cobra.ExactArgs with
// custom Args validators that return withCode(2,...) directly, eliminating
// the need for string matching here. TODO: migrate when convenient.
func isCobraUsageError(err error) bool {
	msg := err.Error()
	return strings.HasPrefix(msg, "accepts ") ||
		strings.HasPrefix(msg, "unknown command") ||
		strings.HasPrefix(msg, "unknown flag") ||
		strings.HasPrefix(msg, "unknown shorthand flag") ||
		strings.HasPrefix(msg, "required flag") ||
		strings.HasPrefix(msg, "invalid argument") // pflag typed-value parse errors
}
