package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Mozilla-Ocho/tabstack-cli/cmd"
)

// exitErr is duplicated minimally here as an interface check so main does not
// need to import internals just for the type. Any error carrying a Code method
// sets the process exit code; everything else falls back to 1.
type coded interface {
	error
	Code() int
}

func main() {
	root := cmd.NewRootCmd()

	if err := root.Execute(); err != nil {
		// cobra already printed usage errors (it returns them with its own
		// handling), but because we set SilenceErrors we print here ourselves
		// so the message goes to stderr consistently.
		fmt.Fprintln(os.Stderr, err)

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
// unknown commands, and unknown flags — all user mistakes that should exit 2.
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
		strings.HasPrefix(msg, "required flag")
}
