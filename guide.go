// Package tabstack holds assets that live at the repository root but ship
// inside the binary. Go's embed cannot reach outside a package's directory, so
// AGENTS.md is embedded here rather than from cmd/.
package tabstack

import _ "embed"

// AgentGuide is AGENTS.md, the reference for driving the CLI from an AI agent.
// Embedding it means `tabstack agent-guide` always prints the copy that matches
// the installed version, with no network or repository checkout needed.
//
//go:embed AGENTS.md
var AgentGuide string
