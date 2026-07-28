package mcp

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// addManageTools registers read-only, session-backed context tools. They go
// through the console client (auth host), which refreshes and rotates the
// session as needed. None of them mutates a key or a session.
func addManageTools(s *sdk.Server, d Deps) {
	sdk.AddTool(s, &sdk.Tool{
		Name:        "whoami",
		Description: "Return the signed-in user, session expiry, default organisation, and the organisations the session can act for.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, whoamiOut, error) {
		if err := d.requireSession(); err != nil {
			return nil, whoamiOut{}, err
		}
		me, err := d.Console.Me(ctx)
		if err != nil {
			return nil, whoamiOut{}, err
		}
		out := whoamiOut{
			Email:      me.User.Email,
			DefaultOrg: me.DefaultOrg,
			ExpiresAt:  me.Session.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
		}
		for _, o := range me.Organizations {
			out.Organizations = append(out.Organizations, orgOut{ID: o.ID, Name: o.Name, Role: o.Role})
		}
		return nil, out, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "list_orgs",
		Description: "List the organisations the signed-in user belongs to.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, listOrgsOut, error) {
		if err := d.requireSession(); err != nil {
			return nil, listOrgsOut{}, err
		}
		orgs, err := d.Console.Organizations(ctx)
		if err != nil {
			return nil, listOrgsOut{}, err
		}
		var out listOrgsOut
		for _, o := range orgs {
			out.Organizations = append(out.Organizations, orgOut{ID: o.ID, Name: o.Name, Role: o.Role})
		}
		return nil, out, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "active_org",
		Description: "Return the organisation whose API key this server is using for product calls.",
	}, func(_ context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, activeOrgOut, error) {
		if d.ActiveOrg == "" {
			return nil, activeOrgOut{}, fmt.Errorf("no active organisation is set")
		}
		return nil, activeOrgOut{ActiveOrg: d.ActiveOrg}, nil
	})
}

// requireSession fails a management tool with sign-in guidance when no session
// client is available, rather than dereferencing a nil.
func (d Deps) requireSession() error {
	if d.Console == nil || d.Console.Session() == nil {
		return fmt.Errorf("not signed in; run `tabstack auth login`")
	}
	return nil
}

type orgOut struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type whoamiOut struct {
	Email         string   `json:"email"`
	DefaultOrg    string   `json:"default_org"`
	ExpiresAt     string   `json:"expires_at"`
	Organizations []orgOut `json:"organizations"`
}

type listOrgsOut struct {
	Organizations []orgOut `json:"organizations"`
}

type activeOrgOut struct {
	ActiveOrg string `json:"active_org"`
}
