package cmd

import (
	"bytes"
	"strings"
	"testing"

	tabstack "github.com/Mozilla-Ocho/tabstack-cli"
)

func TestAgentGuidePrintsEmbeddedDoc(t *testing.T) {
	cmd := newAgentGuideCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if out.String() != tabstack.AgentGuide {
		t.Error("output does not match the embedded guide")
	}
	for _, want := range []string{"# Using `tabstack` from an AI agent", "## When to use tabstack", "## Exit codes"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("guide missing %q", want)
		}
	}
}

func TestAgentGuideNeedsNoClient(t *testing.T) {
	if newAgentGuideCmd().Annotations["skipClient"] != "true" {
		t.Error("agent-guide must work without an API key (skipClient)")
	}
}
