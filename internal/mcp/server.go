// Package mcp runs a local Model Context Protocol server that exposes the
// Tabstack product API as MCP tools over stdio.
//
// It is deliberately a thin adapter: every tool builds a request and calls the
// existing internal/client (product host) or internal/console (auth host)
// method, then hands the result back as MCP content. All product calls use the
// org-scoped API key resolved by the command layer; management tools use the
// session. Nothing here re-resolves credentials or opens the network on its
// own.
//
// Transport is stdio, so stdout carries JSON-RPC frames only. This package must
// never write to stdout; diagnostics belong on stderr and are the command
// layer's job, not a tool handler's.
package mcp

import (
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/console"
)

// serverName is the MCP implementation name advertised to clients.
const serverName = "tabstack"

// Deps are the backends and settings the tools need. The command layer builds
// these once (resolving the API key, minting one from the session if needed)
// and hands them in.
type Deps struct {
	// Product is the authenticated product-host client (extract/generate/agent).
	Product *client.Client
	// Console is the auth-host client with the session attached, used by the
	// management tools. It may be non-nil but session-less, in which case those
	// tools return a "sign in" tool error rather than crashing.
	Console *console.Client
	// SchemasDir is the local schema store the schema tools read from. Empty when
	// the store could not be located; the schema tools then report that.
	SchemasDir string
	// ActiveOrg is the organisation the product key belongs to, surfaced by the
	// active_org tool.
	ActiveOrg string
	// Version is the CLI build version, advertised as the server version.
	Version string
}

// NewServer builds the MCP server with every tool registered.
func NewServer(d Deps) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: serverName, Version: d.Version}, nil)
	addFetchTools(s, d)
	addStreamTools(s, d)
	addSchemaTools(s, d)
	addManageTools(s, d)
	return s
}
