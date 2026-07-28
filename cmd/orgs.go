package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/console"
)

// orgRef is the shape org resolution works on, so the same rules apply whether
// the candidates came from the server or from the local config.
type orgRef struct {
	ID   string
	Name string
	Role string
}

// resolveOrgRef maps a user-supplied selector onto exactly one organisation.
//
// The order is deliberate and stops at the first match: an exact id, then an
// exact case-insensitive name, then a unique case-insensitive name prefix. An
// ambiguous prefix is an error listing every match, and an unknown selector is
// an error listing what the user does have. There is no fuzzy fallback: quietly
// picking "the closest org" would mean acting with the wrong organisation's
// credential.
func resolveOrgRef(orgs []orgRef, arg string) (orgRef, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return orgRef{}, fmt.Errorf("no organisation given")
	}
	if len(orgs) == 0 {
		return orgRef{}, fmt.Errorf("no organisations known locally. Run: tabstack auth login")
	}

	for _, o := range orgs {
		if o.ID == arg {
			return o, nil
		}
	}

	lower := strings.ToLower(arg)
	for _, o := range orgs {
		if strings.ToLower(o.Name) == lower {
			return o, nil
		}
	}

	var matches []orgRef
	for _, o := range orgs {
		if strings.HasPrefix(strings.ToLower(o.Name), lower) {
			matches = append(matches, o)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return orgRef{}, fmt.Errorf("unknown organisation %q. You belong to:\n%s", arg, formatOrgList(orgs))
	default:
		return orgRef{}, fmt.Errorf("organisation %q is ambiguous, it matches:\n%s", arg, formatOrgList(matches))
	}
}

// formatOrgList renders orgs one per line as "  name (id)", for error messages
// where the user needs the id to disambiguate.
func formatOrgList(orgs []orgRef) string {
	var b strings.Builder
	for _, o := range orgs {
		name := o.Name
		if name == "" {
			name = "(unnamed)"
		}
		fmt.Fprintf(&b, "  %s (%s)\n", name, o.ID)
	}
	return strings.TrimRight(b.String(), "\n")
}

// orgRefsFromConsole converts a server org list for resolution.
func orgRefsFromConsole(orgs []console.Org) []orgRef {
	out := make([]orgRef, 0, len(orgs))
	for _, o := range orgs {
		out = append(out, orgRef{ID: o.ID, Name: o.Name, Role: o.Role})
	}
	return out
}

// orgRefsFromConfig converts the locally known orgs for resolution, sorted so
// error output and pickers are stable between runs.
func orgRefsFromConfig(cfg *config.Config) []orgRef {
	out := make([]orgRef, 0, len(cfg.Orgs))
	for id, o := range cfg.Orgs {
		name := ""
		if o != nil {
			name = o.Name
		}
		out = append(out, orgRef{ID: id, Name: name})
	}
	sortOrgRefs(out)
	return out
}

// sortOrgRefs orders by display name, falling back to id.
func sortOrgRefs(orgs []orgRef) {
	sort.Slice(orgs, func(i, j int) bool {
		a, b := strings.ToLower(orgs[i].Name), strings.ToLower(orgs[j].Name)
		if a == b {
			return orgs[i].ID < orgs[j].ID
		}
		return a < b
	})
}

// resolveOrgLocal resolves a --org selector against the config alone. Product
// commands must not make a management call just to work out which stored key to
// use, so this never touches the network.
func resolveOrgLocal(cfg *config.Config, arg string) (string, error) {
	o, err := resolveOrgRef(orgRefsFromConfig(cfg), arg)
	if err != nil {
		return "", err
	}
	return o.ID, nil
}
