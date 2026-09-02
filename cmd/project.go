package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
)

// loadProjectConfig finds and reads the project file for the working
// directory. A malformed or disallowed file is a usage error: it is a
// configuration mistake with security consequences, and silently ignoring it
// would leave the author believing it took effect.
func loadProjectConfig() (*config.ProjectConfig, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil // nothing sensible to search from
	}
	path, err := config.FindProjectConfig(cwd)
	if err != nil {
		return nil, withCode(2, err)
	}
	pc, err := config.LoadProject(path)
	if err != nil {
		return nil, withCode(2, err)
	}
	return pc, nil
}

// applyProjectConfig pushes project settings into the flags this command
// actually has, for any flag the caller did not set explicitly.
//
// Going through pflag rather than the bound variables is deliberate: the
// values are validated by the flag's own parser, so a bad duration or int in
// the project file fails with the same message it would from the command line.
//
// The Changed() check is the crux. Flags now carry meaningful defaults
// (--timeout 2m, --retries 2, --concurrency 4), so without it the default
// would always win and project config would never apply to anything.
func applyProjectConfig(cmd *cobra.Command, pc *config.ProjectConfig) error {
	if pc == nil {
		return nil
	}

	settings := []struct {
		flag  string
		value string
	}{
		{"storage", pc.Storage},
		{"output", pc.Output},
		{"effort", pc.Effort},
		{"geo", pc.Geo},
		{"timeout", pc.Timeout},
		{"max-duration", pc.MaxDuration},
	}
	if pc.Concurrency != nil {
		settings = append(settings, struct {
			flag  string
			value string
		}{"concurrency", strconv.Itoa(*pc.Concurrency)})
	}
	if pc.Retries != nil {
		settings = append(settings, struct {
			flag  string
			value string
		}{"retries", strconv.Itoa(*pc.Retries)})
	}

	for _, s := range settings {
		if s.value == "" {
			continue
		}
		f := cmd.Flags().Lookup(s.flag)
		// Absent is fine: most settings only apply to some commands, and a
		// project pinning --concurrency should not break `auth status`.
		if f == nil || f.Changed {
			continue
		}
		if err := f.Value.Set(s.value); err != nil {
			return withCode(2, fmt.Errorf("project config %s sets %s = %q, which is not valid: %w",
				pc.Path, s.flag, s.value, err))
		}
	}
	return nil
}
