package cmd

import (
	"errors"
	"testing"
)

func TestExitErrUnwrap(t *testing.T) {
	inner := errors.New("boom")
	e := withCode(3, inner)
	if !errors.Is(e, inner) {
		t.Error("errors.Is did not unwrap to inner error")
	}
	if e.Error() != "boom" {
		t.Errorf("Error() = %q, want boom", e.Error())
	}
}

func TestWithCodeNil(t *testing.T) {
	if withCode(2, nil) != nil {
		t.Error("withCode(_, nil) should be nil")
	}
}

func TestReadJSONFileMissing(t *testing.T) {
	if _, err := readJSON("@/no/such/file.json", "--schema"); err == nil {
		t.Error("expected error for missing @file")
	}
}

func TestReadInputFileMissingError(t *testing.T) {
	if _, err := readInput("@/no/such/file", "--schema"); err == nil {
		t.Error("expected error for missing @file")
	}
}
