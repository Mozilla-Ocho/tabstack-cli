package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/console"
)

// memStore is an in-memory CredentialStore for wiring a session into the
// console client without touching disk.
type memStore struct{ cfg *config.Config }

func (m *memStore) Load() (*config.Config, error) { return m.cfg, nil }
func (m *memStore) Save(c *config.Config) error   { m.cfg = c; return nil }
func (m *memStore) Path() string                  { return "(memory)" }

// connect stands up the server with the given deps and returns a connected
// client session.
func connect(t *testing.T, d Deps) *sdk.ClientSession {
	t.Helper()
	srv := NewServer(d)
	ct, st := sdk.NewInMemoryTransports()

	ctx := context.Background()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	c := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// productServer stands up a fake product host and returns a client pointed at
// it.
func productClient(t *testing.T, h http.HandlerFunc) *client.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return client.New("key-test", srv.URL, client.WithHTTPClient(srv.Client()))
}

// consoleWithSession returns a console client with a live session attached.
func consoleWithSession(t *testing.T, url string, h *http.Client) *console.Client {
	t.Helper()
	cfg := &config.Config{Session: &config.Session{
		AccessToken:  "tcli_access",
		RefreshToken: "rt_1",
		ExpiresAt:    time.Now().Add(time.Hour),
	}}
	c := console.New(url, console.WithHTTPClient(h))
	c.AttachSession(&memStore{cfg: cfg}, cfg)
	return c
}

func textOf(t *testing.T, res *sdk.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatalf("result has no content")
	}
	tc, ok := res.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("content[0] = %T, want *TextContent", res.Content[0])
	}
	return tc.Text
}

func TestToolsAreRegistered(t *testing.T) {
	cs := connect(t, Deps{Version: "test"})
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	for _, want := range []string{
		"extract_markdown", "extract_json", "generate_json",
		"automate", "research", "schema_list", "schema_resolve",
		"whoami", "list_orgs", "active_org",
	} {
		if !got[want] {
			t.Errorf("tool %q not registered", want)
		}
	}
}

func TestExtractMarkdownTool(t *testing.T) {
	pc := productClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/extract/markdown" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer key-test" {
			t.Errorf("missing product bearer: %s", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"content":"# Hello","url":"https://example.com"}`))
	})
	cs := connect(t, Deps{Product: pc, Version: "test"})

	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "extract_markdown",
		Arguments: map[string]any{"url": "https://example.com"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool errored: %s", textOf(t, res))
	}
	var out extractMarkdownOut
	if err := json.Unmarshal([]byte(textOf(t, res)), &out); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if out.Content != "# Hello" {
		t.Errorf("content = %q", out.Content)
	}
}

func TestExtractJSONToolWithInlineSchema(t *testing.T) {
	pc := productClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"title":"T"}`))
	})
	cs := connect(t, Deps{Product: pc, Version: "test"})

	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "extract_json",
		Arguments: map[string]any{
			"url":    "https://example.com",
			"schema": `{"type":"object"}`,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool errored: %s", textOf(t, res))
	}
	if got := textOf(t, res); got != `{"title":"T"}` {
		t.Errorf("result = %q, want the schema-defined JSON verbatim", got)
	}
}

func TestExtractJSONRejectsBothSchemas(t *testing.T) {
	cs := connect(t, Deps{Product: productClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("product host should not be called on a local validation error")
	}), Version: "test"})

	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "extract_json",
		Arguments: map[string]any{
			"url":         "https://example.com",
			"schema":      `{"type":"object"}`,
			"schema_name": "job-posting",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected a tool error for mutually-exclusive schema args")
	}
}

func TestWhoamiTool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cli/me" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tcli_access" {
			t.Errorf("missing session bearer: %s", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"user":{"email":"u@example.test"},"session":{"expires_at":"2026-12-31T00:00:00Z"},"default_org":"org_a","organizations":[{"id":"org_a","name":"Alpha","role":"owner"}]}`))
	}))
	t.Cleanup(srv.Close)
	cons := consoleWithSession(t, srv.URL, srv.Client())

	cs := connect(t, Deps{Console: cons, Version: "test"})
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "whoami"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool errored: %s", textOf(t, res))
	}
	var out whoamiOut
	if err := json.Unmarshal([]byte(textOf(t, res)), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Email != "u@example.test" || out.DefaultOrg != "org_a" || len(out.Organizations) != 1 {
		t.Errorf("out = %+v", out)
	}
}

func TestWhoamiWithoutSessionIsToolError(t *testing.T) {
	cs := connect(t, Deps{Version: "test"}) // no console client
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "whoami"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected a tool error when not signed in")
	}
}
