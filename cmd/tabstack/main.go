package main

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"io"
	"os"
	"os/signal"
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

	// Only for failures we cannot explain. A footer on every dropped
	// connection would train people to skip it.
	if cmd.IsLikelyBug(err) {
		_, _ = fmt.Fprint(w, cmd.BugReportHint())
	}
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
		// Every usage error now carries its code on the error itself: the
		// positional validators in cmd/helpers.go, the grouping commands'
		// unknown-subcommand check, and the root FlagErrorFunc all return
		// withCode(2, ...). Anything reaching the fallback is a genuine
		// runtime failure.
		if c, ok := errors.AsType[coded](err); ok {
			os.Exit(c.Code())
		}
		os.Exit(1)
	}
}
