package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
)

func TestReadInputLiteral(t *testing.T) {
	got, err := readInput("hello")
	if err != nil || got != "hello" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestReadInputEmpty(t *testing.T) {
	got, err := readInput("")
	if err != nil || got != "" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestReadInputFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(path, []byte("file contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readInput("@" + path)
	if err != nil || got != "file contents" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestReadInputFileMissing(t *testing.T) {
	_, err := readInput("@/no/such/file/here")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadInputStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig })

	go func() {
		_, _ = w.WriteString("piped data")
		w.Close()
	}()

	got, err := readInput("-")
	if err != nil || got != "piped data" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestReadJSONValid(t *testing.T) {
	got, err := readJSON(`{"a":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"a":1}` {
		t.Errorf("got %s", got)
	}
}

func TestReadJSONInvalid(t *testing.T) {
	_, err := readJSON(`{not json`)
	if err == nil {
		t.Fatal("expected invalid JSON error")
	}
}

func TestReadJSONEmpty(t *testing.T) {
	_, err := readJSON("   ")
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestGeoTarget(t *testing.T) {
	if g := geoTarget(""); g != nil {
		t.Errorf("empty country should yield nil, got %+v", g)
	}
	if g := geoTarget("   "); g != nil {
		t.Errorf("whitespace country should yield nil, got %+v", g)
	}
	g := geoTarget("gb")
	if g == nil || g.Country != "GB" {
		t.Errorf("got %+v, want Country=GB", g)
	}
}

func TestWithCode(t *testing.T) {
	if withCode(3, nil) != nil {
		t.Error("withCode(_, nil) should be nil")
	}
	err := withCode(7, errors.New("boom"))
	var c *exitErr
	if !errors.As(err, &c) {
		t.Fatalf("err = %v, want *exitErr", err)
	}
	if c.Code() != 7 {
		t.Errorf("Code = %d", c.Code())
	}
	if err.Error() != "boom" {
		t.Errorf("Error = %q", err.Error())
	}
	if !errors.Is(err, err) {
		t.Error("unwrap chain broken")
	}
}

func TestClassifyError(t *testing.T) {
	if classifyError(nil) != nil {
		t.Error("nil should classify to nil")
	}

	// API error -> code 3.
	apiErr := &client.APIError{StatusCode: 400, Message: "bad"}
	out := classifyError(apiErr)
	var c *exitErr
	if !errors.As(out, &c) || c.Code() != 3 {
		t.Errorf("API error -> %v, want code 3", out)
	}

	// Generic error -> code 1.
	out = classifyError(errors.New("network down"))
	if !errors.As(out, &c) || c.Code() != 1 {
		t.Errorf("generic error -> %v, want code 1", out)
	}
}

func TestFailureError(t *testing.T) {
	withMsg := failureError("research", "rate limited")
	if withMsg.Error() != "research failed: rate limited" {
		t.Errorf("got %q", withMsg.Error())
	}
	noMsg := failureError("automation", "")
	if noMsg.Error() != "automation reported failure" {
		t.Errorf("got %q", noMsg.Error())
	}
}
