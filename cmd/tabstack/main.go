package main

import (
	"context"
	"errors"
	"image/color"
	"os"
	"strings"

	// fang renders with lipgloss v2; the v1 module (charmbracelet/lipgloss) is
	// used separately by internal/ui for command output.
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/fang"

	"github.com/Mozilla-Ocho/tabstack-cli/cmd"
)

// Tabstack brand palette, sourced from tabstack.ai.
var (
	brandPurple      = lipgloss.Color("#541bff") // primary accent
	brandPurpleLight = lipgloss.Color("#7c5cff") // lighter purple for dark terminals
	brandPink        = lipgloss.Color("#ff97ea") // secondary accent
	brandPinkDeep    = lipgloss.Color("#e688d3") // pink that reads on light terminals
	brandInk         = lipgloss.Color("#10100f") // near-black body text
	brandPaper       = lipgloss.Color("#f4f4f5") // off-white body text on dark
	brandGray        = lipgloss.Color("#7c7985") // muted text
	brandRed         = lipgloss.Color("#d61f69") // error (rose, bridges the pink family)
	brandWhite       = lipgloss.Color("#ffffff")
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

func main() {
	root := cmd.NewRootCmd()

	// fang runs the command tree with styled help, errors, and version output.
	// It prints any error itself (styled, to stderr) and returns it, so we keep
	// owning the exit-code mapping that makes this CLI scriptable.
	if err := fang.Execute(
		context.Background(),
		root,
		fang.WithVersion(cmd.Version()),
		fang.WithColorSchemeFunc(brandScheme),
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
